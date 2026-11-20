#!/usr/bin/env python3
"""
Ecovacs Deebot Ozmo 930 (DG3G / DR930) WiFi Provisioning Script

Puts the robot on your local network without needing the cloud app.
Connect to the robot's AP (ECOVACS_xxxx, IP 192.168.0.1) and run this script.
"""

import argparse
import base64
import json
import socket
import sys

def calc_crc8(data: bytes) -> int:
    crc8_tab = []
    di = 7
    for i in range(256):
        b = i
        for _ in range(8):
            if (b & 128) != 0:
                b = (b << 1) ^ di
            else:
                b = b << 1
        crc8_tab.append(b & 255)

    crc = 0
    for byte in data:
        crc = crc8_tab[(crc ^ byte) & 255]
    return crc

def get_config_buffer(slk_param: str) -> bytes:
    """Mimics EcoCRC8.getConfigBuffer by prepending magic bytes (0x42, 0xA4, 0x59) and CRC8 checksum."""
    data = slk_param.encode("utf-8")
    crc = calc_crc8(data)
    # Magic bytes: 0x42 (66), 0xA4 (-92 signed), 0x59 (89), followed by crc8
    header = bytes([0x42, 0xA4, 0x59, crc])
    return header + data

def connect_robot(ssid: str, passphrase: str, admin_user: str, host_ip: str, robot_ip: str = "192.168.0.1", port: int = 9876, timeout: int = 15):
    # Ensure admin_user has @ecouser.net suffix if not already present
    if "@" not in admin_user:
        admin_user = f"{admin_user}@ecouser.net"

    print(f"[*] Target robot: {robot_ip}:{port}")
    print(f"[*] Provisioning SSID: '{ssid}'")
    print(f"[*] Admin user: '{admin_user}'")
    print(f"[*] Controller/LB host: '{host_ip}'")

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        print(f"[*] Connecting to robot AP at {robot_ip}:{port}...")
        sock.connect((robot_ip, port))
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
        print("[+] Connected successfully!")

        # Calculate append_info length (length of ssid + passphrase)
        append_info = str(len(ssid.encode("utf-8")) + len(passphrase.encode("utf-8")))

        # Prepare slk_msg (XML SetSlkParam payload)
        slk_param = f"<ctl td='SetSlkParam' user='{admin_user}' lb='{host_ip}'/>"
        slk_buffer = get_config_buffer(slk_param)
        slk_message = base64.b64encode(slk_buffer).decode("ascii")

        # Format message: @{"td":"scpa","ssid":"...","passphrase":"...","encrypt":"yes","append_info":"...","slk_msg":"..."}\x00
        payload_dict = {
            "td": "scpa",
            "ssid": ssid,
            "passphrase": passphrase,
            "encrypt": "yes",
            "append_info": append_info,
            "slk_msg": slk_message
        }
        raw_json = json.dumps(payload_dict, separators=(",", ":"))
        message = f"@{raw_json}\x00"

        print(f"[*] Sending provisioning message...")
        sock.sendall(message.encode("utf-8"))

        # Receive response
        raw_data = sock.recv(2048)
        sock.close()

        if not raw_data:
            print("[-] No response received from robot.")
            return False

        data_str = raw_data.decode("utf-8", errors="replace").strip("\x00")
        print(f"[+] Raw response received: {data_str}")

        # Parse JSON response if present
        json_str = data_str.lstrip("@")
        try:
            resp_obj = json.loads(json_str)
            if "resp_msg" in resp_obj:
                decoded_raw = base64.b64decode(resp_obj["resp_msg"])
                # EcoCRC8 buffer strips first 4 bytes (magic + crc8)
                decoded_msg = decoded_raw[4:].decode("utf-8", errors="replace") if len(decoded_raw) > 4 else decoded_raw.decode("utf-8", errors="replace")
                print(f"[+] Decoded resp_msg: {decoded_msg}")
            if resp_obj.get("result") == "ok" or resp_obj.get("ret") == "ok":
                print("\n[SUCCESS] Robot accepted WiFi configuration!")
                print(f"The robot should now disconnect from AP mode and join '{ssid}'.")
                return True
        except json.JSONDecodeError:
            pass

        print("\n[INFO] Configuration sent. Check your router/DHCP leases to confirm the robot connects.")
        return True

    except socket.timeout:
        print(f"[-] Connection timed out while trying to reach {robot_ip}:{port}.")
        print("    Ensure your computer is connected to the robot's WiFi AP (ECOVACS_xxxx).")
        return False
    except Exception as e:
        print(f"[-] Error: {e}")
        return False

def parse_args():
    parser = argparse.ArgumentParser(
        description="Ecovacs Ozmo 930 WiFi Pairing & Provisioning Tool",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter
    )
    parser.add_argument("--ssid", required=True, help="WiFi network SSID")
    parser.add_argument("--passphrase", required=True, help="WiFi password")
    parser.add_argument("--admin-user", default="admin@ecouser.net", help="Admin username (default: admin@ecouser.net)")
    parser.add_argument("--host-ip", default="192.168.10.178", help="IP address of local controller / DNS sink (iot-jail host)")
    parser.add_argument("--robot-ip", default="192.168.0.1", help="Robot IP in AP pairing mode")
    parser.add_argument("--port", type=int, default=9876, help="Robot TCP provisioning port")
    parser.add_argument("-i", "--interactive", action="store_true", help="Prompt interactively for parameters")
    return parser.parse_args()

if __name__ == "__main__":
    args = parse_args()

    if args.interactive:
        ssid = input(f"SSID [{args.ssid}]: ").strip() or args.ssid
        passphrase = input(f"Passphrase: ").strip() or args.passphrase
        admin_user = input(f"Admin user [{args.admin_user}]: ").strip() or args.admin_user
        host_ip = input(f"Host IP [{args.host_ip}]: ").strip() or args.host_ip
        robot_ip = input(f"Robot IP [{args.robot_ip}]: ").strip() or args.robot_ip
        port = args.port
    else:
        ssid = args.ssid
        passphrase = args.passphrase
        admin_user = args.admin_user
        host_ip = args.host_ip
        robot_ip = args.robot_ip
        port = args.port

    connect_robot(ssid, passphrase, admin_user, host_ip, robot_ip=robot_ip, port=port)
