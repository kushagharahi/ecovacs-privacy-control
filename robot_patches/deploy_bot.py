#!/usr/bin/env python3
"""
On-Bot Stack Deployment Tool for Ecovacs OZMO 930 (Telnet + BusyBox nc)
=======================================================================
Deploys the unified `ecovacs-bot` ARMv7 binary (ecovacs-controller + ha-bridge
+ embedded MQTT broker) compressed as `/data/ecovacs-bot.gz` so it extracts to
`/tmp/ecovacs-bot` in RAM at boot and runs with zero external server.
"""

import argparse
import gzip
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
import time

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(SCRIPT_DIR)


def calc_md5(path):
    h = hashlib.md5()
    with open(path, "rb") as f:
        while chunk := f.read(65536):
            h.update(chunk)
    return h.hexdigest()


def build_and_gzip(out_dir):
    bin_path = os.path.join(out_dir, "ecovacs-bot-linux-armv7")
    gz_path = os.path.join(out_dir, "ecovacs-bot.gz")
    env = {
        **os.environ,
        "CGO_ENABLED": "0",
        "GOOS": "linux",
        "GOARCH": "arm",
        "GOARM": "7",
    }
    print("[BUILD] Compiling unified ARMv7 binary (./cmd/ecovacs-bot)...")
    res = subprocess.run(
        ["go", "build", "-buildvcs=false", "-ldflags=-s -w", "-o", bin_path, "./cmd/ecovacs-bot"],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
    )
    if res.returncode != 0:
        print(f"[ERROR] Build failed:\n{res.stderr}")
        sys.exit(1)

    with open(bin_path, "rb") as f_in, gzip.open(gz_path, "wb", compresslevel=9) as f_out:
        shutil.copyfileobj(f_in, f_out)

    print(
        f"[BUILD] Raw: {os.path.getsize(bin_path) / (1024 * 1024):.2f} MiB, "
        f"Gzip: {os.path.getsize(gz_path) / (1024 * 1024):.2f} MiB -> {gz_path}"
    )
    return gz_path


class TelnetSession:
    def __init__(self, host, port=23, timeout=8):
        self.host = host
        self.port = port
        self.timeout = timeout
        self.sock = None

    def connect(self):
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect((self.host, self.port))
        time.sleep(0.3)
        self.read_available()

    def close(self):
        if self.sock:
            try:
                self.sock.close()
            except Exception:
                pass
            self.sock = None

    def read_available(self, wait_secs=0.4):
        self.sock.settimeout(wait_secs)
        buffer = b""
        start = time.time()
        while time.time() - start < wait_secs:
            try:
                data = self.sock.recv(8192)
                if not data:
                    break
                buffer += data
            except socket.timeout:
                break
            except Exception:
                break
        return buffer.decode("utf-8", errors="ignore")

    def run_cmd(self, cmd, wait_secs=0.6):
        self.read_available(wait_secs=0.05)
        self.sock.sendall((cmd.strip() + "\r\n").encode("utf-8"))
        time.sleep(wait_secs)
        return self.read_available(wait_secs=wait_secs)

    def send_file(self, local_path, remote_path, data_port=4446, stage_path=None, expected_md5=None):
        with open(local_path, "rb") as f:
            data = f.read()

        tmp_path = stage_path or f"{remote_path}.tmp"
        self.run_cmd(f"rm -f {tmp_path} {remote_path}.tmp")
        self.sock.sendall(f"nc -l -p {data_port} > {tmp_path}\r\n".encode("utf-8"))
        time.sleep(0.4)

        data_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        data_sock.settimeout(15)
        data_sock.connect((self.host, data_port))
        data_sock.sendall(data)
        data_sock.close()
        time.sleep(0.5)

        if expected_md5:
            md5_out = self.run_cmd(f"md5sum {tmp_path}")
            if expected_md5 not in md5_out:
                self.run_cmd(f"rm -f {tmp_path}")
                raise RuntimeError(f"Checksum mismatch in {tmp_path}: {md5_out}")
            if stage_path:
                # /data is 7.7 MiB total; remove old archive only after verified /tmp copy.
                self.run_cmd(f"rm -f {remote_path}")

        self.run_cmd(f"mv {tmp_path} {remote_path} && chmod 644 {remote_path}", wait_secs=3.0)


def generate_autostart_script(robot_sn):
    env_lines = [
        "SERVER_IP=127.0.0.1",
        "ROBOT_IP=127.0.0.1",
        "PROTOCOL=xmpp",
        "MQTT_LISTEN=:1883",
        "MQTT_BROKER=tcp://127.0.0.1:1883",
        "CERT_CACHE_DIR=/data/ecovacs-certs",
    ]
    if robot_sn:
        env_lines.append(f"ROBOT_SN={robot_sn}")

    env_block = " \\\n        ".join(env_lines)
    return f"""#!/bin/sh
BIN=/tmp/ecovacs-bot
ARCHIVE=/data/ecovacs-bot.gz
LOG=/tmp/ecovacs-bot.log

stop_bot() {{
    killall ecovacs-bot 2>/dev/null
    rm -f /tmp/ecovacs-bot.pid
}}

start_bot() {{
    stop_bot
    if [ ! -x "$BIN" ] && [ -f "$ARCHIVE" ]; then
        zcat "$ARCHIVE" > "${{BIN}}.tmp" && chmod 755 "${{BIN}}.tmp" && mv "${{BIN}}.tmp" "$BIN"
    fi
    if [ -x "$BIN" ]; then
        {env_block} \\
        "$BIN" -log-file="$LOG" -log-max-kb=256 </dev/null >/dev/null 2>&1 &
        echo $! > /tmp/ecovacs-bot.pid
    fi
}}

case "$1" in
  stop)
    stop_bot
    ;;
  restart)
    rm -f "$BIN"
    start_bot
    ;;
  *)
    start_bot
    ;;
esac
"""


def parse_custom_sounds(raw):
    if not raw:
        return None
    raw = raw.strip()
    if raw.startswith("{"):
        return json.loads(raw)
    out = {}
    for pair in raw.split(","):
        if ":" in pair:
            k, v = pair.split(":", 1)
            out[k.strip()] = v.strip()
    return out


def print_status(session, expected_md5=None):
    print("\n--- On-Bot Stack Status ---")
    print(session.run_cmd("df -h /data /tmp; ls -lh /data/ecovacs-bot.gz /data/autostart/25-ecovacs-bot.sh /data/custom_sounds.json /data/config/medusa/lb.conf /tmp/ecovacs-bot 2>/dev/null", wait_secs=0.8).strip())
    if expected_md5:
        out = session.run_cmd("md5sum /data/ecovacs-bot.gz 2>/dev/null")
        status = "MATCH" if expected_md5 in out else f"MISMATCH ({out.strip()})"
        print(f"\n[MD5] Local: {expected_md5} -> Remote: {status}")
    print("\n[PROCESS]:")
    print(session.run_cmd("ps w | grep -E 'ecovacs-bot|medusa' | grep -v grep", wait_secs=0.6).strip())
    print("\n[HEALTH & PORTS]:")
    print(session.run_cmd("netstat -ltn 2>/dev/null | grep -E ':(1883|5223|8000|8007) ' || true", wait_secs=0.6).strip())
    print(session.run_cmd("wget -q -O - http://127.0.0.1:8000/health 2>/dev/null || echo 'health unavailable'", wait_secs=0.6).strip())
    print("\n[LB CONF]:")
    print(session.run_cmd("cat /data/config/medusa/lb.conf 2>/dev/null", wait_secs=0.5).strip())


def main():
    parser = argparse.ArgumentParser(description="Deploy unified ecovacs-bot stack directly onto OZMO 930")
    parser.add_argument("artifact", nargs="?", help="Path to pre-built ecovacs-bot.gz (or raw binary)")
    parser.add_argument("--build", action="store_true", help="Cross-compile ARMv7 ecovacs-bot and gzip locally")
    parser.add_argument("--robot-ip", default=os.getenv("ROBOT_IP"), required=not os.getenv("ROBOT_IP"), help="Robot IP address")
    parser.add_argument("--port", type=int, default=23, help="Telnet port (default: 23)")
    parser.add_argument("--robot-sn", default=os.getenv("ROBOT_SN", ""), help="Robot serial number")
    parser.add_argument("--custom-sounds", default=os.getenv("CUSTOM_SOUNDS", ""), help="Custom sounds JSON or id:name,id:name")
    parser.add_argument("--restart", action="store_true", help="Restart /data/autostart/25-ecovacs-bot.sh immediately")
    parser.add_argument("--lb-hot-switch", action="store_true", help="Update /data/config/medusa/lb.conf to 127.0.0.1 and switch running medusa via mdsctl")
    parser.add_argument("--verify-only", action="store_true", help="Inspect robot status without modifying files")
    args = parser.parse_args()

    artifact_path = args.artifact
    if args.build:
        out_dir = os.path.join(REPO_ROOT, "cmd", "ecovacs-bot")
        artifact_path = build_and_gzip(out_dir)

    if not args.verify_only:
        if not artifact_path or not os.path.isfile(artifact_path):
            print("[ERROR] Provide path to ecovacs-bot.gz or pass --build")
            sys.exit(1)
        if not artifact_path.endswith(".gz"):
            gz_path = artifact_path + ".gz"
            with open(artifact_path, "rb") as f_in, gzip.open(gz_path, "wb", compresslevel=9) as f_out:
                shutil.copyfileobj(f_in, f_out)
            artifact_path = gz_path

    expected_md5 = calc_md5(artifact_path) if artifact_path and os.path.isfile(artifact_path) else None

    print(f"[CONNECT] Connecting to {args.robot_ip}:{args.port}...")
    session = TelnetSession(args.robot_ip, port=args.port)
    session.connect()
    print("[CONNECTED] Root shell acquired.")

    try:
        if args.verify_only:
            print_status(session, expected_md5)
            return

        print(f"[TRANSFER] Uploading {artifact_path} -> /data/ecovacs-bot.gz (MD5 {expected_md5})...")
        session.send_file(
            artifact_path,
            "/data/ecovacs-bot.gz",
            stage_path="/tmp/ecovacs-bot.gz.tmp",
            expected_md5=expected_md5,
        )
        md5_out = session.run_cmd("md5sum /data/ecovacs-bot.gz")
        if expected_md5 not in md5_out:
            print(f"[ERROR] Checksum mismatch after upload! Output: {md5_out}")
            sys.exit(1)
        print("[SUCCESS] /data/ecovacs-bot.gz checksum verified.")

        sounds_map = parse_custom_sounds(args.custom_sounds)
        if sounds_map:
            sounds_json = json.dumps(sounds_map, indent=2)
            print("[CONFIG] Writing /data/custom_sounds.json...")
            session.run_cmd("cat << 'EOF_SOUNDS' > /data/custom_sounds.json\n" + sounds_json + "\nEOF_SOUNDS")

        print("[CONFIG] Installing /data/autostart/25-ecovacs-bot.sh...")
        autostart_sh = generate_autostart_script(args.robot_sn)
        session.run_cmd("mkdir -p /data/autostart /data/ecovacs-certs /data/config/medusa")
        session.run_cmd("cat << 'EOF_AUTO' > /data/autostart/25-ecovacs-bot.sh\n" + autostart_sh + "\nEOF_AUTO")
        session.run_cmd("chmod 755 /data/autostart/25-ecovacs-bot.sh")

        if args.restart:
            print("[START] Starting on-bot stack (/data/autostart/25-ecovacs-bot.sh restart)...")
            session.run_cmd("/data/autostart/25-ecovacs-bot.sh restart", wait_secs=2.5)

        if args.lb_hot_switch:
            print("[MEDUSA] Pointing /data/config/medusa/lb.conf -> 127.0.0.1...")
            session.run_cmd('printf \'{\\n\\t"lbSvr":\\t"127.0.0.1"\\n}\\n\' > /data/config/medusa/lb.conf')
            print("[MEDUSA] Hot-switching running medusa XMPP target to 127.0.0.1:5223 via mdsctl...")
            out = session.run_cmd('/usr/bin/mdsctl /tmp/mds_cmd.sock wk \'{"todo":"updateBestXmppSvr","ip":"127.0.0.1","port":5223}\'', wait_secs=1.5)
            print("[MEDUSA] mdsctl output:", out.strip())

        print_status(session, expected_md5)
    finally:
        session.close()


if __name__ == "__main__":
    main()
