# Ecovacs OZMO 930 Info
Also known as DG3G / DR930 / Class 115 / ZJ1602

## Hardware Overview
- **CPU / SoC**: Rockchip RK3188+ (Quad-core ARM Cortex-A9, `RK30xx` platform)
- **RAM**: 512 MiB DDR3 @ 300MHz
- **Storage**: NAND Flash with 7 MTD partitions:
  - `uboot`, `boot_flag`, `boot1`, `boot1_fs` (squashfs rootfs), `boot2`, `boot2_fs` (squashfs backup), `data_fs` (writable ext4 for `/data`)
- **Internal Daemon**: `medusa` (configured via `/etc/conf/medusa/deebot_dr930.conf`)
- **WiFi**: Realtek RTL8723BS / RTL8188 802.11n USB/SDIO module

---

## Protocol & Cloud Communication Architecture
Unlike the Deebot 900/901 series (which connects directly to MQTT on port 8883), the **OZMO 930 uses XMPP + HTTP discovery**:

1. **WiFi Provisioning (TCP Port 9876)**:
   - When the reset button is pressed, the robot spins up AP mode `ECOVACS_xxxx` at `192.168.0.1`.
   - The app sends a TCP packet to port 9876:
     `@{"td":"scpa","ssid":"<SSID>","passphrase":"<PASS>","encrypt":"yes","append_info":"<LEN>","slk_msg":"<BASE64>"}\x00`
   - `slk_msg` wraps `<ctl td='SetSlkParam' user='admin@ecouser.net' lb='<CONTROLLER_IP>'/>` prefixed with the 4-byte EcoCRC8 header (`0x42, 0xA4, 0x59, <CRC8>`).
   - The robot saves `<CONTROLLER_IP>` as its Load Balancer (`LB`).

2. **Server Discovery (HTTP Port 8007)**:
   - On boot, the robot executes `/etc/conf/medusa/mds_wukong_get_best_iot_svr.sh`.
   - It issues an HTTP POST request:
     `POST http://<LB>:8007/lookup.do`
     `Body: {"todo":"FindBest","service":"EcoMsgNew"}`
   - The server **must** return JSON without spaces:
     `{"result":"ok","ip":"<SERVER_IP>","port":5223}`

3. **Control Channel (TLS XMPP Port 5223)**:
   - The robot establishes a TLS connection to `<SERVER_IP>:5223`.
   - XML commands (`<ctl td="..."/>`) are transmitted inside XMPP IQ stanzas:
     `<iq type="set" from="..." to="..."><query xmlns="com:ctl"><ctl td="Clean" .../></query></iq>`
   - Responses and status telemetry are published back via `<iq>` / `<message>` elements.

4. **Local Robot Services**:
   - `0.0.0.0:8111`: BOSH (HTTP-bound XMPP) server (`BoshSvr0`).
   - `0.0.0.0:3232`: Unicast UDP command channel (`Udp1`).
   - `0.0.0.0:2375`: Unicast UDP discovery (`Udp0`).
   - `226.1.1.1:1900`: Multicast discovery group (`Mcast0`).

---

## Root Access & Persistence Secrets

### 1. USB Auto-Execution Vulnerability (`/usr/bin/udisk.sh`)
The robot's udev configuration (`/usr/lib/udev/rules.d/usb-storage.rules`) automatically invokes `/usr/bin/udisk.sh start %k` whenever a SCSI/USB storage device is plugged into the Micro-USB / service port.

Inspecting `/usr/bin/udisk.sh`:
```sh
PRODUCT=`cjp "${RET}" string product`  # from /etc/fw.manifest
if [ -f ${USB_MNT}/${PRODUCT}/${PRODUCT}.sh ]; then
    sh ${USB_MNT}/${PRODUCT}/${PRODUCT}.sh $1
fi
```
**Exploit**:
1. Format a USB drive as FAT32.
2. Create directories for all product names: `/dr930/`, `/dg3g/`, `/zj1602/`.
3. In each directory, place `<product>.sh` (e.g. `dr930/dr930.sh`):
   ```sh
   #!/bin/sh
   # Mount data partition if not mounted
   mkdir -p /data/autostart
   # Drop persistent telnet daemon
   cat << 'EOF' > /data/autostart/telnet.sh
   #!/bin/sh
   /usr/sbin/telnetd -l /bin/sh &
   EOF
   chmod +x /data/autostart/telnet.sh
   /usr/sbin/telnetd -l /bin/sh &
   ```
4. Plug into the vacuum via USB OTG adapter and reboot. Root shell spawns immediately on port 23!

### 2. Built-in Autostart Mechanism (`/etc/rc.d/autostart.sh`)
During every boot, `/etc/rc.sysinit` executes `/etc/rc.d/autostart.sh`, which has:
```sh
APPS=`ls /data/autostart/*.sh`
for apps in ${APPS}; do
    ${apps} start
done
```
Since `/data` is on a read-write ext4 partition (`/dev/block/mtd/by-name/data_fs`), any script placed in `/data/autostart/*.sh` will run automatically on boot with full `root` privileges.

### 3. Dynamic Root Password (`eco_passwd`)
On newer firmware versions, the root password is generated dynamically per-device:
$$\text{base64}\left(\text{sha256}\left(\{model\}\text{d4:3d:7e:fa:12:5d:C8:02:8F:0A:E2:F5}\{sn\}\textbackslash n\right)\right)$$
Can also be calculated using Dennis Giese's tool: `https://builder.dontvacuum.me/ecopassword`.
Older rootfs dumps use the static crypt DES hash: `root:zTfMqeON.Edcg`.

---

## Wifi Setup Without App
1. Press and hold the reset button under the cover until the robot beeps and announces it is in pairing mode.
2. Connect your laptop / machine to the robot's AP network (`ECOVACS_xxxx`).
3. Run the provisioning script:
   ```bash
   python3 ozmo-930-wifi.py --ssid "<YOUR_SSID>" --passphrase "<YOUR_WIFI_PASSWORD>" --host-ip 192.168.10.178
   ```
4. The robot will disconnect from AP mode and join your local network.

---

## Kernal output via UART

 set serial.0 lc r = 0x13 timeout
[  456.232843] Restarting system.
DDR Version 1.04 20130830
In
SRX
DDR3
300MHz
Bus Width=32 Col=10 Bank=8 Row=14 CS=1 Die Bus-Width=16 Size=512MB
OUT
Boot1 Release Time: 2016-03-11, version: 2.31
ChipType = 0x4, 1322
No.1 FLASH ID:98 f1 80 15 72 16
StorageInit ok = 46599
Load uboot, ReadLba = 2000
Load OK, addr=0x60200000, size=0x24800


U-Boot 2014.01-gb7ecbca (Jun 09 2017 - 09:05:46)

ChipVer: 310B20130131V101
CPU:	RK3188+
Board:	RK30xx platform Board
total reserving memory(except stack) is :48m
DRAM:  512 MiB
arch_interrupt_init
DMAInit: OK
storage init OK!
Using default environment

read_env StorageUbootDataLoad failed at 0
In:    serial
Out:   serial
Err:   serial
board_late_init
unsigned!
no sys part.
SecureBootEn = 0 0
##################################################
uboot version: U-Boot 2014.01-gb7ecbca (Jun 09 2017 - 09:05:46)

#Boot ver:

##################################################
setup gpio int
++++++++GetBootMode++++++++
LMemApiReadLba:0
++++++++buf++++++++ =boot_mode1
boot_mode1!!!!!!
GetParam
LMemApiReadLba:0
check parameter success
rootfstype : squashfs
OLD CMDLINE: console=ttyFIQ0,115200 console=tty0 init=/sbin/init mac_addr=de:ad:de:ad:be:ef initrd=0x62000000,0x00800000  mtdparts=rk29xxnand:0x00001000@0x00002000(uboot),0x00001000@0x00003000(boot_flag),0x00002500@0x00004000(boot1),0x00014000@0x00006500(boot1_fs),0x00002500@0x0001a500(boot2),0x00014000@0x0001d000(boot2_fs),-@0x00031000(data_fs)
bootMedia : nandflash
NEW CMDLINE: console=ttyFIQ0,115200 console=tty0 init=/sbin/init mac_addr=de:ad:de:ad:be:ef initrd=0x62000000,0x00800000  mtdparts=rk29xxnand:0x00001000@0x00002000(uboot),0x00001000@0x00003000(boot_flag),0x00002500@0x00004000(boot1),0x00014000@0x00006500(boot1_fs),0x00002500@0x0001a500(boot2),0x00014000@0x0001d000(boot2_fs),-@0x00031000(data_fs) root=/dev/block/mtd/by-name/boot1_fs rootfstype=squashfs
partition(uboot): offset=0x00002000, size=0x00001000
partition(boot_flag): offset=0x00003000, size=0x00001000
partition(boot1): offset=0x00004000, size=0x00002500
partition(boot1_fs): offset=0x00006500, size=0x00014000
partition(boot2): offset=0x0001A500, size=0x00002500
partition(boot2_fs): offset=0x0001D000, size=0x00014000
partition(data_fs): offset=0x00031000, size=0xFFFFFFFF
empty serial no.
Writing env to storage...
write_env StorageUbootDataStore failed at 0
failed
checkKey
vbus = 0
lcd_ctrl_init [38]
i2c init OVER in board!
set_lcdc_dclk: lcdc_source_clk = 768, clk = 71, div = 11
rk30_load_screen for lcdc ok!
lcd_enable [55]
ERROR: [fastboot_find_ptn]: partition(misc) not found, aborting
table:
misc partition not found!
boot form boot1!!!!
LMemApiReadLba:0
LMemApiReadLba:0
LMemApiReadLba:0
SecureBootCheckOK:0
booti: do_bootm_linux...

Starting kernel ...

[    0.000000] Initializing cgroup subsys cpu
[    0.000000] Linux version 3.0.36+ (jack@jack-pc) (gcc version 4.6.x-google 20120106 (prerelease) (GCC) ) #6 SMP PREEMPT Fri Nov 10 11:20:20 CST 2017
[    0.000000] CPU: ARMv7 Processor [413fc090] revision 0 (ARMv7), cr=10c5387d
[    0.000000] CPU: VIPT nonaliasing data cache, VIPT aliasing instruction cache
[    0.000000] Machine: RK30board
[    0.000000] Ignoring unrecognised tag 0x00000000
[    0.000000] Memory policy: ECC disabled, Data cache writeback
[    0.000000] bootconsole [earlycon0] enabled
[    0.000000] CPU SRAM: copied sram code from c08c6000 to fef00100 - fef02018
[    0.000000] CPU SRAM: copied sram data from c08c7f18 to fef02018 - fef02790
[    0.000000] sram_log: @ 2   `" 2q    : 4q    :     4q ?#     :     4q ?&     :     4q
[    0.000000] CLKDATA_MSG: pll_flag = 0x03
[    0.000000] L310 cache controller enabled
[    0.000000] l2x0: 16 ways, CACHE_ID 0x4100c0c8, AUX_CTRL 0x76050001, Cache size: 524288 B
[    0.000000] DDR DEBUG: version 1.00 20131106
[    0.000000] DDR DEBUG: DDR3 Device
[    0.000000] DDR DEBUG: Bus Width=32 Col=10 Bank=8 Row=14 CS=1 Total Capability=512MB
[    0.000000] DDR DEBUG: init success!!! freq=300MHz
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x3, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: ZERR=0, ZDONE=0, ZPD=0x0, ZPU=0x0, OPD=0x0, OPU=0x0
[    0.000000] DDR DEBUG: DRV Pull-Up=0xb, DRV Pull-Dwn=0xb
[    0.000000] DDR DEBUG: ODT Pull-Up=0x2, ODT Pull-Dwn=0x2
[    0.000000] Boot mode: REBOOT (6) flag:  (0x00000000)
[    0.000000] PERCPU: Embedded 7 pages/cpu @c11ee000 s6688 r8192 d13792 u32768
[    0.000000] Built 1 zonelists in Zone order, mobility grouping on.  Total pages: 130048
[    0.000000] Kernel command line: console=ttyFIQ0,115200 console=tty0 init=/sbin/init mac_addr=de:ad:de:ad:be:ef initrd=0x62000000,0x00190000  mtdparts=rk29xxnand:0x00001000@0x00002000(uboot),0x00001000@0x00003000(boot_flag),0x00002500@0x00004000(boot1),0x00014000@0x00006500(boot1_fs),0x00002500@0x0001a500(boot2),0x00014000@0x0001d000(boot2_fs),-@0x00031000(data_fs) root=/dev/block/mtd/by-name/boot1_fs rootfstype=squashfs androidboot.bootloader=fastboot fb.addr=0x63c08000 chipver=310B20130131V101 androidboot.serialno=0123456789
[    0.000000] PID hash table entries: 2048 (order: 1, 8192 bytes)
[    0.000000] Dentry cache hash table entries: 65536 (order: 6, 262144 bytes)
[    0.000000] Inode-cache hash table entries: 32768 (order: 5, 131072 bytes)
[    0.000000] allocated 2097152 bytes of page_cgroup
[    0.000000] please try 'cgroup_disable=memory' option if you don't want memory cgroups
[    0.000000] Memory: 512MB = 512MB total
[    0.000000] Memory: 501740k/501740k available, 22548k reserved, 24576K highmem
[    0.000000] Virtual kernel memory layout:
[    0.000000]     vector  : 0xffff0000 - 0xffff1000   (   4 kB)
[    0.000000]     fixmap  : 0xfff00000 - 0xfffe0000   ( 896 kB)
[    0.000000]     DMA     : 0xff600000 - 0xffe00000   (   8 MB)
[    0.000000]     vmalloc : 0xdf000000 - 0xfe800000   ( 504 MB)
[    0.000000]     lowmem  : 0xc0000000 - 0xde800000   ( 488 MB)
[    0.000000]     pkmap   : 0xbfe00000 - 0xc0000000   (   2 MB)
[    0.000000]     modules : 0xbf000000 - 0xbfe00000   (  14 MB)
[    0.000000]       .init : 0xc0408000 - 0xc042d000   ( 148 kB)
[    0.000000]       .text : 0xc042d000 - 0xc0870e58   (4368 kB)
[    0.000000]       .data : 0xc0872000 - 0xc08c5c40   ( 336 kB)
[    0.000000]        .bss : 0xc08c9024 - 0xc11ec928   (9359 kB)
[    0.000000] SLUB: Genslabs=13, HWalign=32, Order=0-3, MinObjects=0, CPUs=4, Nodes=1
[    0.000000] Preemptible hierarchical RCU implementation.
[    0.000000] 	Verbose stalled-CPUs detection is disabled.
[    0.000000] NR_IRQS:352
[    0.000000] rk30_gpio_init: 128 gpio irqs in 4 banks
[    0.000000] sched_clock: 32 bits at 24MHz, resolution 41ns, wraps every 178956ms
[    0.000000] rk_timer: version 1.3
[    0.000000] Console: colour dummy device 80x30
[    0.000000] console [tty0] enabled, bootconsole disabled
<hit enter to activate fiq debugger>
[    0.000000] Initializing cgroup subsys cpu
[    0.000000] Linux version 3.0.36+ (jack@jack-pc) (gcc version 4.6.x-google 20120106 (prerelease) (GCC) ) #6 SMP PREEMPT Fri Nov 10 11:20:20 CST 2017
[    0.000000] CPU: ARMv7 Processor [413fc090] revision 0 (ARMv7), cr=10c5387d
[    0.000000] CPU: VIPT nonaliasing data cache, VIPT aliasing instruction cache
[    0.000000] Machine: RK30board
[    0.000000] Ignoring unrecognised tag 0x00000000
[    0.000000] Memory policy: ECC disabled, Data cache writeback
[    0.000000] bootconsole [earlycon0] enabled
[    0.000000] CPU SRAM: copied sram code from c08c6000 to fef00100 - fef02018
[    0.000000] CPU SRAM: copied sram data from c08c7f18 to fef02018 - fef02790
[    0.000000] sram_log: @ 2   `" 2q    : 4q    :     4q ?#     :     4q ?&     :     4q
[    0.000000] CLKDATA_MSG: pll_flag = 0x03
[    0.000000] L310 cache controller enabled
[    0.000000] l2x0: 16 ways, CACHE_ID 0x4100c0c8, AUX_CTRL 0x76050001, Cache size: 524288 B
[    0.000000] DDR DEBUG: version 1.00 20131106
[    0.000000] DDR DEBUG: DDR3 Device
[    0.000000] DDR DEBUG: Bus Width=32 Col=10 Bank=8 Row=14 CS=1 Total Capability=512MB
[    0.000000] DDR DEBUG: init success!!! freq=300MHz
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x3, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: DTONE=0x1, DTERR=0x0, DTIERR=0x0, DTPASS=0x4, DGSL=1 extra clock, DGPS=90
[    0.000000] DDR DEBUG: ZERR=0, ZDONE=0, ZPD=0x0, ZPU=0x0, OPD=0x0, OPU=0x0
[    0.000000] DDR DEBUG: DRV Pull-Up=0xb, DRV Pull-Dwn=0xb
[    0.000000] DDR DEBUG: ODT Pull-Up=0x2, ODT Pull-Dwn=0x2
[    0.000000] Boot mode: REBOOT (6) flag:  (0x00000000)
[    0.000000] PERCPU: Embedded 7 pages/cpu @c11ee000 s6688 r8192 d13792 u32768
[    0.000000] Built 1 zonelists in Zone order, mobility grouping on.  Total pages: 130048
[    0.000000] Kernel command line: console=ttyFIQ0,115200 console=tty0 init=/sbin/init mac_addr=de:ad:de:ad:be:ef initrd=0x62000000,0x00190000  mtdparts=rk29xxnand:0x00001000@0x00002000(uboot),0x00001000@0x00003000(boot_flag),0x00002500@0x00004000(boot1),0x00014000@0x00006500(boot1_fs),0x00002500@0x0001a500(boot2),0x00014000@0x0001d000(boot2_fs),-@0x00031000(data_fs) root=/dev/block/mtd/by-name/boot1_fs rootfstype=squashfs androidboot.bootloader=fastboot fb.addr=0x63c08000 chipver=310B20130131V101 androidboot.serialno=0123456789
[    0.000000] PID hash table entries: 2048 (order: 1, 8192 bytes)
[    0.000000] Dentry cache hash table entries: 65536 (order: 6, 262144 bytes)
[    0.000000] Inode-cache hash table entries: 32768 (order: 5, 131072 bytes)
[    0.000000] allocated 2097152 bytes of page_cgroup
[    0.000000] please try 'cgroup_disable=memory' option if you don't want memory cgroups
[    0.000000] Memory: 512MB = 512MB total
[    0.000000] Memory: 501740k/501740k available, 22548k reserved, 24576K highmem
[    0.000000] Virtual kernel memory layout:
[    0.000000]     vector  : 0xffff0000 - 0xffff1000   (   4 kB)
[    0.000000]     fixmap  : 0xfff00000 - 0xfffe0000   ( 896 kB)
[    0.000000]     DMA     : 0xff600000 - 0xffe00000   (   8 MB)
[    0.000000]     vmalloc : 0xdf000000 - 0xfe800000   ( 504 MB)
[    0.000000]     lowmem  : 0xc0000000 - 0xde800000   ( 488 MB)
[    0.000000]     pkmap   : 0xbfe00000 - 0xc0000000   (   2 MB)
[    0.000000]     modules : 0xbf000000 - 0xbfe00000   (  14 MB)
[    0.000000]       .init : 0xc0408000 - 0xc042d000   ( 148 kB)
[    0.000000]       .text : 0xc042d000 - 0xc0870e58   (4368 kB)
[    0.000000]       .data : 0xc0872000 - 0xc08c5c40   ( 336 kB)
[    0.000000]        .bss : 0xc08c9024 - 0xc11ec928   (9359 kB)
[    0.000000] SLUB: Genslabs=13, HWalign=32, Order=0-3, MinObjects=0, CPUs=4, Nodes=1
[    0.000000] Preemptible hierarchical RCU implementation.
[    0.000000] 	Verbose stalled-CPUs detection is disabled.
[    0.000000] NR_IRQS:352
[    0.000000] rk30_gpio_init: 128 gpio irqs in 4 banks
[    0.000000] sched_clock: 32 bits at 24MHz, resolution 41ns, wraps every 178956ms
[    0.000000] rk_timer: version 1.3
[    0.000000] Console: colour dummy device 80x30
[    0.000000] console [tty0] enabled, bootconsole disabled
[    0.014703] Calibrating delay loop (skipped) preset value.. 1631.46 BogoMIPS (lpj=8157341)
[    0.014733] pid_max: default: 32768 minimum: 301
[    0.014894] Security Framework initialized
[    0.014980] Mount-cache hash table entries: 512
[    0.015580] Initializing cgroup subsys cpuacct
[    0.015633] Initializing cgroup subsys memory
[    0.015670] Initializing cgroup subsys freezer
[    0.015712] CPU: Testing write buffer coherency: ok
[    0.096579] CPU1: Booted secondary processor
[    0.136569] CPU2: Booted secondary processor
[    0.176572] CPU3: Booted secondary processor
[    0.176614] Brought up 4 CPUs
[    0.176648] SMP: Total of 4 processors activated (6525.87 BogoMIPS).
[    0.177003] devtmpfs: initialized
[    0.183049] NET: Registered protocol family 16
[    0.183546] GRF_GPIO3D_PULL before val=0x6a96,++++++++++++++++++
[    0.183568] GRF_GPIO3D_PULL after val=0x6a96,+++++++++++++++++++
[    0.183811] rk29sdk_wifi_bt_gpio_control_init: request wifi power gpio failed
[    0.185335] console [ttyFIQ0] enabled
[    0.185465] Registered FIQ tty driver de0b1c00
[    0.185986] Loaded driver for PL330 DMAC-1 rk29-pl330
[    0.186011] 	DBUFF-32x8bytes Num_Chans-6 Num_Peri-12 Num_Events-12
[    0.186189] Loaded driver for PL330 DMAC-2 rk29-pl330
[    0.186211] 	DBUFF-64x8bytes Num_Chans-7 Num_Peri-20 Num_Events-14
[    0.688725] rk29xx_spim: driver initialized, fifo_len=32,bus_num=1
[    0.700197] bio: create slab <bio-0> at 0
[    0.704562] SCSI subsystem initialized
[    0.704683] usbcore: registered new interface driver usbfs
[    0.704773] usbcore: registered new interface driver hub
[    0.719123] usbcore: registered new device driver usb
[    0.719418] rk30_i2c rk30_i2c.0: i2c-0: RK30 I2C adapter
[    0.719634] rk30_i2c rk30_i2c.1: i2c-1: RK30 I2C adapter
[    0.719842] Advanced Linux Sound Architecture Driver Version 1.0.24.
[    0.720537] i2c i2c-1: No ack, Maybe slave(addr: 0x5a) not exist or abnormal power-on, retry 2...
[    0.720809] i2c i2c-1: No ack, Maybe slave(addr: 0x5a) not exist or abnormal power-on, retry 1...
[    0.721074] i2c i2c-1: No ack, Maybe slave(addr: 0x5a) not exist or abnormal power-on, retry 0...
[    0.721106] The device is not act8846
[    0.721153] i2c-core: driver [act8846] using legacy suspend method
[    0.721176] i2c-core: driver [act8846] using legacy resume method
[    0.721223] rk808_i2c_probe,line=1014
[    0.725337] rk808_pre_init,line=15
[    0.730762] rk808_pre_init,line=148
[    0.730782] rk808_irq_init,line=151
[    0.798169] rk808_set_init,line=155
[    0.798196] rk808_set_init:g_pmic_type=4
[    0.802126] rk808_set_init  vdd_cpu =1100000uV end
[    0.806141] rk808_set_init  vdd_core =1100000uV end
[    0.806734] rk808_set_init  rk_dcdc3 =1200000uV end
[    0.808596] rk808_set_init  rk_dcdc4 =3300000uV end
[    0.810462] rk808_set_init  rk_ldo1 =3300000uV end
[    0.812224] rk808_set_init  rk_ldo2 =3300000uV end
[    0.813987] rk808_set_init  rk_ldo3 =1000000uV end
[    0.815749] rk808_set_init  rk_ldo4 =1800000uV end
[    0.817513] rk808_set_init  rk_ldo5 =2800000uV end
[    0.819274] rk808_set_init  rk_ldo6 =1200000uV end
[    0.821037] rk808_set_init  rk_ldo7 =1800000uV end
[    0.822797] rk808_set_init  rk_ldo8 =3300000uV end
[    0.822822] rk808_set_init,line=208 END
[    0.822889] i2c-core: driver [rk808] using legacy suspend method
[    0.822912] i2c-core: driver [rk808] using legacy resume method
[    0.823099] rk808_rtc_probe,line=528
[    0.830239] rk808-rtc rk808-rtc: rtc core: registered rk808 as rtc0
[    0.831803] rk808_rtc_probe:ok
[    0.831869] Switching to clocksource rk_timer
[    0.835930] Switched to NOHz mode on CPU #0
[    0.836610] Switched to NOHz mode on CPU #2
[    0.836619] Switched to NOHz mode on CPU #3
[    0.836628] Switched to NOHz mode on CPU #1
[    0.846055] 1953..sdmmc_is_selected_emmc: RK SDMMC is not setted to support eMMC.
[    0.846097] rk29_sdmmc_probe: internal_storage is NOT emmc.
[    0.846258] NET: Registered protocol family 2
[    0.846402] IP route cache hash table entries: 4096 (order: 2, 16384 bytes)
[    0.846700] TCP established hash table entries: 16384 (order: 5, 131072 bytes)
[    0.846975] TCP bind hash table entries: 16384 (order: 5, 196608 bytes)
[    0.847263] TCP: Hash tables configured (established 16384 bind 16384)
[    0.847286] TCP reno registered
[    0.847306] UDP hash table entries: 256 (order: 1, 8192 bytes)
[    0.847343] UDP-Lite hash table entries: 256 (order: 1, 8192 bytes)
[    0.847671] NET: Registered protocol family 1
[    0.847840] Unpacking initramfs...
[    1.718103] Freeing initrd memory: 1600K
[    1.718218] PMU: registered new PMU device of type 0
[    1.718343] efuse_version_val: efuse version = 51
[    1.718367] DVFS MSG: core: dvfs_adjust_table_lmtvolt get leakage_level = 4000
[    1.718394] efuse_version_val: efuse version = 51
[    1.718414] DVFS MSG: aclk_gpu: dvfs_adjust_table_lmtvolt get leakage_level = 4000
[    1.718726] DVFS WARNING:	vd_core : selector=64,but volt <=0
[    1.719285] DVFS WARNING:	vd_cpu : selector=64,but volt <=0
[    1.719340] cpufreq: delete arm freq(1608000)
[    1.766835] rk3188 cpufreq version 2.2, suspend freq 816 MHz
[    1.771449] VPU_SERVICE: failed on clk_get pd_video
[    1.771476] VPU_SERVICE: failed on clk_get aclk_ddr_vepu
[    1.771492] VPU_SERVICE: failed on clk_get hclk_cpu_vcodec
[    1.771510] vpu: power on
[    1.771551] VPU_SERVICE: checking hw id 4831
[    1.772782] vpu: power off...done
[    1.772815] VPU_SERVICE: init success
[    1.802301] highmem bounce pool size: 64 pages
[    1.809847] squashfs: version 4.0 (2009/01/31) Phillip Lougher
[    1.810101] fuse init (API version 7.16)
[    1.810296] msgmni has been set to 935
[    1.823902] Block layer SCSI generic (bsg) driver version 0.4 loaded (major 252)
[    1.823928] io scheduler noop registered
[    1.823938] io scheduler deadline registered
[    1.823986] io scheduler cfq registered (default)
[    1.824148] rk_serial.c v1.6 2013-12-23
[    1.824227] rk_serial.0: ttyS0 at MMIO 0x10124000 (irq = 66) is a rk29_serial.0
[    1.824357] rk_serial rk_serial.0: membase 0xdf828000
[    1.824400] rk_serial.1: ttyS1 at MMIO 0x10126000 (irq = 67) is a rk29_serial.1
[    1.824510] rk_serial rk_serial.1: membase 0xdf82c000
[    1.824552] rk_serial.3: ttyS3 at MMIO 0x20068000 (irq = 69) is a rk29_serial.3
[    1.824652] rk_serial rk_serial.3: membase 0xdf830000
[    1.824777] [drm] Initialized drm 1.1.0 20060810
[    1.824888] Rockchip ion module(version: 1.0) is successfully loaded
[    1.826552] loop: module loaded
[    1.827040] usbcore: registered new interface driver kaweth
[    1.827079] usbcore: registered new interface driver asix
[    1.827111] usbcore: registered new interface driver cdc_ether
[    1.827145] usbcore: registered new interface driver dm9601
[    1.827178] usbcore: registered new interface driver dm9620
[    1.827213] usbcore: registered new interface driver SR9700_android
[    1.827262] usbcore: registered new interface driver CoreChips
[    1.827304] usbcore: registered new interface driver smsc75xx
[    1.827344] usbcore: registered new interface driver smsc95xx
[    1.827376] usbcore: registered new interface driver plusb
[    1.827398] Initializing USB Mass Storage driver...
[    1.827438] usbcore: registered new interface driver usb-storage
[    1.827450] USB Mass Storage support registered.
[    2.049096] DWC_OTG: ^^^^^^^^^^^^^^^^^^Host Mode
[    2.049128] usb20_otg usb20_otg: DWC OTG Controller
[    2.049211] usb20_otg usb20_otg: new USB bus registered, assigned bus number 1
[    2.049258] usb20_otg usb20_otg: irq 48, io mem 0x00000000
[    2.049295] DWC_OTG: Init: Port Power? op_state=1
[    2.049306] DWC_OTG: Init: Power Port (0)
[    2.049355] usb usb1: New USB device found, idVendor=1d6b, idProduct=0002
[    2.049372] usb usb1: New USB device strings: Mfr=3, Product=2, SerialNumber=1
[    2.049388] usb usb1: Product: DWC OTG Controller
[    2.049399] usb usb1: Manufacturer: Linux 3.0.36+ dwc_otg_hcd
[    2.049411] usb usb1: SerialNumber: usb20_otg
[    2.049738] hub 1-0:1.0: USB hub found
[    2.049760] hub 1-0:1.0: 1 port detected
[    2.052177] DWC_OTG: host20_driver_probe host2.0 reg addr: 0x101c0000 remap:0xdf900000
[    2.262514] DWC_OTG: ^^^^^^^^^^^^^^^^^^Host Mode
[    2.262541] usb20_host usb20_host: DWC OTG Controller
[    2.262595] usb20_host usb20_host: new USB bus registered, assigned bus number 2
[    2.262631] usb20_host usb20_host: irq 49, io mem 0x00000000
[    2.262681] DWC_OTG: Init: Port Power? op_state=1
[    2.262692] DWC_OTG: Init: Power Port (0)
[    2.262731] usb usb2: New USB device found, idVendor=1d6b, idProduct=0002
[    2.262747] usb usb2: New USB device strings: Mfr=3, Product=2, SerialNumber=1
[    2.262762] usb usb2: Product: DWC OTG Controller
[    2.262773] usb usb2: Manufacturer: Linux 3.0.36+ host20_hcd
[    2.262786] usb usb2: SerialNumber: usb20_host
[    2.263077] hub 2-0:1.0: USB hub found
[    2.263098] hub 2-0:1.0: 1 port detected
[    2.263434] rk29-keypad: probe of rk29-keypad failed with error -22
[    2.263697] using rtc device, rk808, for alarms
[    2.263770] i2c /dev entries driver
[    2.264002] Linux video capture interface: v2.00
[    2.264178] usbcore: registered new interface driver uvcvideo
[    2.264192] USB Video Class driver (v1.1.0)
[    2.358862] cpuidle: using governor ladder
[    2.358872] cpuidle: using governor menu
[    2.404205]
[    2.404207] drivers/mmc/core/core.c...2019..  ===== mmc_rescan Begin....[mmc0]
[    2.404214] .Line4018..The End of SDMMC-probe Ver.6.07 The last modify date is 2013-12-20.  [sd_mmc]
[    2.404336] .Line4018..The End of SDMMC-probe Ver.6.07 The last modify date is 2013-12-20.  [sdio]
[    2.404397] Skipping unavailable LED gpio -1 (button-backlight)
[    2.436862]
[    2.436864] =================
[    2.436866] sdmmc_rescan..2026..  ====find no SDMMC host. [mmc0]
[    2.447316]
[    2.447317] drivers/mmc/core/core.c...2019..  ===== mmc_rescan Begin....[mmc1]
[    2.471937] rk29_sdmmc_change_clk_div..1684..  newDiv=40, newCLK=300Khz [sdio]
[    2.502572] asoc: nau8810-hifi <-> rk29_i2s.1 mapping ok
[    2.502919] ALSA device list:
[    2.502933]   #0: nau8810-audio
[    2.503022] Netfilter messages via NETLINK v0.30.
[    2.503074] nf_conntrack version 0.5.0 (7864 buckets, 31456 max)
[    2.503327] ctnetlink v0.93: registering with nfnetlink.
[    2.503606] ip_tables: (C) 2000-2006 Netfilter Core Team
[    2.503746] arp_tables: (C) 2002 David S. Miller
[    2.503797] TCP cubic registered
[    2.503807] Initializing XFRM netlink socket
[    2.503825] NET: Registered protocol family 17
[    2.503847] NET: Registered protocol family 15
[    2.503859] Registering the dns_resolver key type
[    2.503893] VFP support v0.3: implementor 41 architecture 3 part 30 variant 9 rev 4
[    2.503927] Registering SWP/SWPB emulation handler
[    2.573250] ddrfreq: verion 3.2 20131126
[    2.573267] ddrfreq: fix cpu pause bug
[    2.573280] ddrfreq: normal 396MHz video 300MHz video_low 0MHz dualview 0MHz idle 0MHz suspend 198MHz reboot 300MHz
[    2.574376] ddrfreq: change freq to 384 MHz when normal
[    2.576692] rk808-rtc rk808-rtc: setting system clock to 2024-06-22 03:15:05 UTC (1719026105)
[    2.576866] Freeing init memory: 148K
[    2.639911] gp_rknand_info = : 0xde11eb00
[    2.643997] rk30_chip_set:1
[    2.646933] FtlInit!
[    2.649142] No.1 FLASH ID:98 f1 80 15 72 16
[    2.654091] FTL version: 5.0.31 20160918
[    2.691951] usb 2-1: new high speed USB device number 2 using usb20_host
[    2.703730] gBootMedia = 0 gSdmmcFwPartOffset =0!
[    2.708444] rknand_buffer.c version: 4.40 20130830!
[    2.713450] 7 cmdlinepart partitions found on MTD device rk29xxnand
[    2.719725] Creating 7 MTD partitions on "rk29xxnand":
[    2.724870] 0x000000400000-0x000000600000 : "uboot"
[    2.730270] 0x000000600000-0x000000800000 : "boot_flag"
[    2.735904] 0x000000800000-0x000000ca0000 : "boot1"
[    2.741187] 0x000000ca0000-0x0000034a0000 : "boot1_fs"
[    2.746763] 0x0000034a0000-0x000003940000 : "boot2"
[    2.752064] 0x000003a00000-0x000006200000 : "boot2_fs"
[    2.757664] 0x000006200000-0x000006cc0000 : "data_fs"
[    2.902821] usb 2-1: New USB device found, idVendor=0bda, idProduct=f179
[    2.902853] usb 2-1: New USB device strings: Mfr=1, Product=2, SerialNumber=3
[    2.902870] usb 2-1: Product: 802.11n
[    2.902879] usb 2-1: Manufacturer: Realtek
[    2.902890] usb 2-1: SerialNumber: 0C9A421F4D15
[    3.164518] udevd[194]: starting version 182
[    3.329751] EXT4-fs (mtdblock6): couldn't mount as ext3 due to feature incompatibilities
[    3.372217] EXT4-fs (mtdblock6): couldn't mount as ext2 due to feature incompatibilities
[    3.445625] EXT4-fs (mtdblock6): mounted filesystem with ordered data mode. Opts: (null)
[    3.561978] usb 2-1: USB disconnect, device number 2

deboot login: UART_IER = 0x85
UART_IIR = 0xc4
UART_LSR = 0xc1
UART_MSR = 0x0
UART_MCR = 0x3
UART_RFL = 0x1f
UART_LCR = 0x13
UART_IER = 0x85
UART_IIR = 0xc6
UART_LSR = 0xc3
UART_MSR = 0x0
UART_MCR = 0x3
UART_RFL = 0x20
UART_LCR = 0x13
UART_IER = 0x85
UART_IIR = 0xc4
UART_LSR = 0xc3
UART_MSR = 0x0
UART_MCR = 0x3
UART_RFL = 0x20
UART_LCR = 0x13
UART_IER = 0x85
UART_IIR = 0xc4
UART_LSR = 0xc1
UART_MSR = 0x0
UART_MCR = 0x3
UART_RFL = 0x20
UART_LCR = 0x13

deboot login: