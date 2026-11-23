#!/usr/bin/env python3
"""
Firmware Patch Deployment Tool for Ecovacs Robots (Telnet Only)
=============================================================
Builds and deploys ONLY the specified patch(es) or directory directly
over Telnet with dynamic LD_PRELOAD wrapper generation.

Usage:
  # Deploy a specific patch by name or directory:
  python3 robot_patches/deploy.py libsilentgoto --robot-ip 192.168.x.x

  # Recompile ONLY the specified patch then deploy:
  python3 robot_patches/deploy.py libsilentgoto --build --robot-ip 192.168.x.x

  # Point to any external patch directory:
  python3 robot_patches/deploy.py /path/to/my_patch --build --robot-ip 192.168.x.x

  # Deploy multiple specified patches:
  python3 robot_patches/deploy.py libsilentgoto libpersistmap --build --robot-ip 192.168.x.x

  # If no patch is specified, defaults to all patches in robot_patches/:
  python3 robot_patches/deploy.py --robot-ip 192.168.x.x

  # Verify currently installed patches on the robot:
  python3 robot_patches/deploy.py --robot-ip 192.168.x.x --verify-only
"""

import argparse
import base64
import hashlib
import os
import socket
import subprocess
import sys
import time

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))


def calc_md5(path):
    h = hashlib.md5()
    with open(path, "rb") as f:
        while chunk := f.read(65536):
            h.update(chunk)
    return h.hexdigest()


def resolve_target(target):
    """Resolve target to absolute path (local dir, file, or name in SCRIPT_DIR)."""
    # 1. Exact path
    if os.path.exists(target):
        return os.path.abspath(target)
    # 2. Inside SCRIPT_DIR
    cand = os.path.join(SCRIPT_DIR, target)
    if os.path.exists(cand):
        return os.path.abspath(cand)
    # 3. Target without .so
    if target.endswith(".so"):
        cand2 = os.path.join(SCRIPT_DIR, target[:-3])
        if os.path.exists(cand2):
            return os.path.abspath(cand2)
    return None


def build_target(target_path):
    """Build only the specified target directory if Makefile exists."""
    target_path = os.path.abspath(target_path)
    if os.path.isdir(target_path):
        mf = os.path.join(target_path, "Makefile")
        if os.path.isfile(mf):
            print(f"[BUILD] Building only: {os.path.basename(target_path)}...")
            res = subprocess.run(["make", "-C", target_path, "all"], capture_output=True, text=True)
            if res.returncode != 0:
                print(f"[ERROR] Build failed for {target_path}:\n{res.stderr}")
                return False
            print(res.stdout.strip())
            return True
        else:
            print(f"[BUILD] No Makefile in {target_path}, skipping compilation.")
            return True
    elif os.path.isfile(target_path) and target_path.endswith(".so"):
        parent = os.path.dirname(target_path)
        if os.path.isfile(os.path.join(parent, "Makefile")):
            print(f"[BUILD] Building target directory: {os.path.basename(parent)}...")
            res = subprocess.run(["make", "-C", parent, "all"], capture_output=True, text=True)
            if res.returncode != 0:
                print(f"[ERROR] Build failed:\n{res.stderr}")
                return False
            print(res.stdout.strip())
            return True
    return True


def find_patches_in_targets(resolved_targets):
    """Find .so files within the specified resolved target paths only."""
    patches = {}
    for target in resolved_targets:
        if os.path.isfile(target) and target.endswith(".so"):
            fname = os.path.basename(target)
            patches[fname] = {
                "name": fname,
                "local_path": target,
                "remote_path": f"/data/{fname}",
                "md5": calc_md5(target),
            }
        elif os.path.isdir(target):
            for root, dirs, files in os.walk(target):
                dirs[:] = [d for d in dirs if not d.startswith(".")]
                for f in files:
                    if f.endswith(".so") and f not in patches:
                        full_path = os.path.join(root, f)
                        patches[f] = {
                            "name": f,
                            "local_path": full_path,
                            "remote_path": f"/data/{f}",
                            "md5": calc_md5(full_path),
                        }
    return [patches[k] for k in sorted(patches.keys())]


class TelnetSession:
    def __init__(self, host, port=23, timeout=5):
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

    def read_available(self, wait_secs=0.3):
        self.sock.settimeout(wait_secs)
        buffer = b""
        start = time.time()
        while time.time() - start < wait_secs:
            try:
                data = self.sock.recv(4096)
                if not data:
                    break
                buffer += data
            except socket.timeout:
                break
            except Exception:
                break
        return buffer.decode("utf-8", errors="ignore")

    def run_cmd(self, cmd, wait_secs=0.5):
        self.read_available(wait_secs=0.05)
        self.sock.sendall((cmd.strip() + "\r\n").encode("utf-8"))
        time.sleep(wait_secs)
        return self.read_available(wait_secs=wait_secs)

    def send_file(self, local_path, remote_path, data_port=4444):
        """Fast byte-exact binary transfer using robot busybox nc listener over TCP."""
        with open(local_path, "rb") as f:
            data = f.read()

        # Clean old tmp and launch nc listener on robot via telnet
        self.run_cmd(f"rm -f {remote_path}.tmp")
        self.sock.sendall(f"nc -l -p {data_port} > {remote_path}.tmp\r\n".encode("utf-8"))
        time.sleep(0.3)

        # Connect directly to the listener port and blast raw binary bytes
        data_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        data_sock.settimeout(5)
        data_sock.connect((self.host, data_port))
        data_sock.sendall(data)
        data_sock.close()
        time.sleep(0.3)

        # Move to destination and make executable
        self.run_cmd(f"mv {remote_path}.tmp {remote_path} && chmod 755 {remote_path}")


def generate_medusa_wrapper():
    """Dynamically preloads all .so libraries present in /data on the robot."""
    return """#!/bin/sh
# Auto-generated wrapper for medusa with dynamic LD_PRELOAD patches
stop_script(){
\tPID_WK=`pidof mds_wukong_get_best_iot_svr.sh`
\t[ $? -eq 0 ] && kill $PID_WK && sleep 0.5
}

load () {
\trm -f /tmp/mds_cmd.sock
\tPRELOADS=$(ls /data/*.so 2>/dev/null | tr '\\n' ' ')
\tLD_PRELOAD="${PRELOADS}" medusa -f /etc/conf/medusa/deebot_dr930.conf </dev/null >/dev/null 2>&1 &
}

unload () {
\tstop_script
\tkillall -9 medusa 2>/dev/null
\trm -f /tmp/medusa.pid /tmp/lock.file /tmp/mds_cmd.sock 2>/dev/null
\tkillall wget 2>/dev/null
\tsleep 1
}

case "$1" in
      start)
            load
             ;;
       stop)
            unload
             ;;
       restart)
            unload
            load
             ;;
        *)
             echo "$0 <start/stop/restart>"
             ;;
esac
"""


def generate_preload_hook():
    return """#!/bin/sh
WRAPPER="/data/rc.d/medusa.sh"
TARGET="/etc/rc.d/medusa.sh"

case "$1" in
  stop)
    umount "$TARGET" 2>/dev/null
    ;;
  *)
    if [ -f "$WRAPPER" ]; then
        if ! mount | grep -q "$TARGET"; then
            mount --bind "$WRAPPER" "$TARGET"
        fi

        MEDUSA_PID=$(pidof medusa | awk '{print $1}')
        if [ -n "$MEDUSA_PID" ]; then
            if ! grep -q "LD_PRELOAD" "/proc/$MEDUSA_PID/environ" 2>/dev/null; then
                killall -9 medusa 2>/dev/null
                rm -f /tmp/medusa.pid /tmp/lock.file 2>/dev/null
                sleep 1
                "$TARGET" start
            fi
        fi
    fi
    ;;
esac
"""


def print_status(session, patches):
    print("\n--- Robot Patch & Hook Status ---")
    out = session.run_cmd("ls -lh /data/*.so /data/rc.d/medusa.sh /data/autostart/*.sh 2>/dev/null")
    print("[INSTALLED FILES IN /data]:")
    print(out.strip() or "  (None)")

    if patches:
        remote_list = " ".join(p["remote_path"] for p in patches)
        md5_out = session.run_cmd(f"md5sum {remote_list} 2>/dev/null")
        print("\n[CHECKSUMS FOR SPECIFIED PATCHES]:")
        for p in patches:
            status = "MATCH" if p["md5"] and p["md5"] in md5_out else "MISMATCH / MISSING"
            print(f"  {p['name']:25} Local: {p['md5']} -> Remote: {status}")

    mount_out = session.run_cmd("mount | grep medusa.sh")
    print("\n[BIND MOUNT]:")
    print("  " + (mount_out.strip() if mount_out.strip() else "Inactive"))

    pid_out = session.run_cmd("pidof medusa")
    pids = [tok for tok in pid_out.split() if tok.isdigit()]
    pid = pids[0] if pids else None
    if pid:
        env_out = session.run_cmd(f'tr "\\0" "\\n" < /proc/{pid}/environ 2>/dev/null | grep LD_PRELOAD')
        maps_out = session.run_cmd(f'grep -E "/data/.*\\.so" /proc/{pid}/maps 2>/dev/null')
        print(f"\n[RUNNING PROCESS (PID {pid})]:")
        print("  " + (env_out.strip() if env_out.strip() else "No LD_PRELOAD active."))
        print("\n[MAPPED PATCH LIBRARIES]:")
        print("  " + (maps_out.strip() if maps_out.strip() else "No /data/*.so mapped in memory."))
    else:
        print("\n[RUNNING PROCESS]:")
        print("  Medusa process is NOT running!")


def main():
    parser = argparse.ArgumentParser(
        description="Ecovacs Robot Firmware Patch Deployment Tool (Telnet Only)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Examples:\n"
               "  python3 robot_patches/deploy.py libsilentgoto --build --robot-ip 192.168.1.50\n"
               "  python3 robot_patches/deploy.py /path/to/my_patch --build --robot-ip 192.168.1.50\n"
               "  python3 robot_patches/deploy.py libsilentgoto libpersistmap --robot-ip 192.168.1.50\n"
               "  python3 robot_patches/deploy.py --verify-only --robot-ip 192.168.1.50\n"
    )
    parser.add_argument("targets", nargs="*", help="Specific patch name(s), directory, or .so file (defaults to robot_patches/ if omitted)")
    parser.add_argument("--robot-ip", default=os.getenv("ROBOT_IP"), required=not os.getenv("ROBOT_IP"), help="IP address of the robot")
    parser.add_argument("--port", type=int, default=23, help="Telnet port (default: 23)")
    parser.add_argument("--build", action="store_true", help="Compile ONLY the specified patch(es) before deploying")
    parser.add_argument("--verify-only", action="store_true", help="Check status without modifying files")
    parser.add_argument("--restart-medusa", action="store_true", help="Restart medusa immediately after deployment")

    args = parser.parse_args()

    raw_targets = args.targets or [SCRIPT_DIR]
    resolved_targets = []

    for t in raw_targets:
        resolved = resolve_target(t)
        if not resolved:
            print(f"[ERROR] Target not found: {t}")
            sys.exit(1)
        resolved_targets.append(resolved)

    # 1. Build ONLY the specified targets if requested
    if args.build:
        for t in resolved_targets:
            if not build_target(t):
                sys.exit(1)

    # 2. Discover .so patches only within the specified targets
    patches = find_patches_in_targets(resolved_targets)
    print(f"[TARGETS] Specified {len(resolved_targets)} target(s): {[os.path.basename(t) for t in resolved_targets]}")
    print(f"[FOUND] {len(patches)} patch library/libraries to deploy:")
    for p in patches:
        print(f"  - {p['name']} ({p['local_path']}) [MD5: {p['md5']}]")

    print(f"\n[CONNECT] Connecting to robot at {args.robot_ip}:{args.port} via Telnet...")
    session = TelnetSession(args.robot_ip, port=args.port)
    try:
        session.connect()
    except Exception as e:
        print(f"[ERROR] Failed to connect: {e}")
        sys.exit(1)

    print("[CONNECTED] Root shell acquired.")

    if args.verify_only:
        print_status(session, patches)
        session.close()
        sys.exit(0)

    if not patches:
        print("[ERROR] No .so patches found to deploy for specified targets. Use --build to compile first.")
        session.close()
        sys.exit(1)

    try:
        session.run_cmd("mkdir -p /data/rc.d /data/autostart /data/FILES")

        # 3. Transfer ONLY the specified patches directly over Telnet
        for p in patches:
            print(f"[TRANSFER] Sending {p['name']} -> {p['remote_path']}...")
            session.send_file(p["local_path"], p["remote_path"])

        # 4. Deploy medusa wrapper that preloads all deployed /data/*.so
        print("[CONFIG] Deploying medusa.sh wrapper...")
        session.run_cmd("umount /etc/rc.d/medusa.sh 2>/dev/null")
        wrapper_code = generate_medusa_wrapper()
        session.run_cmd("cat << 'EOF_WRAPPER' > /data/rc.d/medusa.sh\n" + wrapper_code + "\nEOF_WRAPPER")
        session.run_cmd("chmod 755 /data/rc.d/medusa.sh")
        session.run_cmd("mount --bind /data/rc.d/medusa.sh /etc/rc.d/medusa.sh")

        # 5. Deploy autostart preload hook
        print("[CONFIG] Deploying autostart hook (/data/autostart/30-medusa-preload.sh)...")
        hook_code = generate_preload_hook()
        session.run_cmd("cat << 'EOF_HOOK' > /data/autostart/30-medusa-preload.sh\n" + hook_code + "\nEOF_HOOK")
        session.run_cmd("chmod 755 /data/autostart/30-medusa-preload.sh")

        # 6. Verify checksums for the specified patches
        remote_list = " ".join(p["remote_path"] for p in patches)
        remote_md5s = session.run_cmd(f"md5sum {remote_list}")
        all_matched = all(p["md5"] in remote_md5s for p in patches)
        if all_matched:
            print("[SUCCESS] Checksums verified for all specified patches on robot!")
        else:
            print(f"[WARNING] Checksum verification output:\n{remote_md5s}")

        # 7. Optional restart
        if args.restart_medusa:
            print("[ACTIVATE] Ensuring bind-mount and restarting medusa...")
            session.run_cmd("if ! mount | grep -q /etc/rc.d/medusa.sh; then mount --bind /data/rc.d/medusa.sh /etc/rc.d/medusa.sh; fi")
            session.run_cmd("/data/rc.d/medusa.sh restart", wait_secs=3)
        else:
            print("[INFO] Patches installed to /data. They will activate automatically on boot, or use --restart-medusa to activate now.")

        print_status(session, patches)

    finally:
        session.close()

    print("\n[COMPLETE] Deployment finished.")


if __name__ == "__main__":
    main()
