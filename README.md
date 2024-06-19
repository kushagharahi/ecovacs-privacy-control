# ecovacs-privacy-control
Privacy first API control for Ecovacs MQTT based vaccums -- tested with Ecovacs Deebot 900/901 series and OZMO 930 _only_

Goal: Control Ecovacs MQTT based vaccum robots directly with a self-hosted local API.

## Usage

- Setup WIFI credentials on robot
  - Hold reset button down robot until you here beep or prompt asking to you go to the app
  - Connect to robot WIFI
  - Send a REST request with `s` as the SSID you want the robot to connect to and `p` being the WIFI password for the SSID
  - `curl -X POST -v \
  http://192.168.0.1:8888/rcp.do \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: Dalvik/2.1.0 (Linux; U; Android 14; Android SDK built for arm64 Build/UE1A.230829.036.A1)' \
  -H 'Connection: Keep-Alive' \
  -H 'Accept-Encoding: gzip' \
  -d '{"td":"SetApConfig","s":"<SSID_HERE>","p":"<PASSWORD_HERE>","u":"ia1a0bodd640d6a2","sc":"b0"}'`
- Build/Run Docker container - Must expose container port 8883
- Point DNS for `ecouser.net` to server running container
- Restart robot -- Robot caches DNS response and to get a refreshed DNS you must restart

If the bot is successful in connecting, you should see something like this in the logs:

```
1637629641: New connection from <snip> on port 8883.
1637629641: New client connected from <snip> as <snip>-<snip>-<snip>-<snip>-<snip>@ls1ok3/<snip> (p2, c1, k120, u'<snip>').
```
## API

`:8000/getMapData` will return the stored map

![map data](/getMapData.jpg)

## TODO:

- Create MQTT Broker with TLS config needed for bot to connect to local server ✅
- Connect robot to WIFI without app ✅
- Document MQTT message schema/contents for sending bot commands ⚠ (WIP)
- Document MQTT message schema/content for receiving bot info ⚠ (WIP)
- Create local API to send/read MQTT messages ⚠ (WIP)

## Technicials

Once setup, MQTT based Ecovacs robot vaccums directly connect to `mq-ww.ecouser.net`:8883 MQTT server. With a proper DNS setup and a self signed cert for `ecouser.net`, users can redirect bot MQTT traffic to a self-hosted MQTT server

ecovacs-privacy-control is a docker container that generates self-signed certificates for `ecouser.net` and launches Mosquitto (MQTT broker)


### MQTT Communication
Topic Level Variables (Unique per device)
- `$bot_serial` - This is the bots unique serial - format looks something like this `b11fceaf-5173-4190-be6e-9c37ef3dc238`
- `$device_type` - This is the type of bot - format looks something like this `ls1ok3`, `131`, or `Deepo9`
- `$resource` - This is unique to the bot? The format on mine looks like `zwzq`
- `$x` - Any string (for publishing messages for the bot to react to)

Payload XML elements
- `ts` - timestamp
- `td` - functionality

Auto Bot Published - Docked
| Function | Topic | Payload | Notes |
|-|-|-|-|
| Battery Info | `iot/atr/BatteryInfo/$bot_serial/$device_type/$resource/x` | `<ctl ts='1644594388465' td='BatteryInfo'><battery power='100'/></ctl>` | - |
| Charge State | `iot/atr/ChargeState/$bot_serial/$device_type/$resource/x` | `<ctl ts='1644594388466' td='ChargeState'><charge type='SlotCharging' h='' r='' s='' g='0'/></ctl>` | `type` can be `Going`, `SlotCharging`, `WireCharging`, `Idle` |
| Sleep Status | `iot/atr/SleepStatus/$bot_serial/$device_type/$resource/x` | `<ctl ts='1644594398677' td='SleepStatus' st='1'/>` | |

Auto Bot Published - Cleaning
| Function | Topic | Payload | Notes |
|-|-|-|-|
| BigDataCleanInfoReport | | | |
| CleanedMap | | | |
| CleanedMapSet | | | |
| CleanedPos | | | |
| CleanedTrace | | | |
| CleanReport | | | |
| CleanReportServer | | | |
| CleanSt | | | |
| errors | | | |
| MapP | | | |
| MapSt | | | |
| Pos | | | |
| trace | | | |

Back and forth communication with bot
| Function | Publishing Topic | Publishing Payload | Response Topic | Response Payload | Notes |
|-|-|-|-|-|-|
| Get software version | `iot/p2p/GetWKVer/$x/$x/$x/$bot_serial/$device_type/$resource/p/$x/j` | `{}` | `iot/p2p/GetWKVer/$bot_serial/$device_type/$resource/$x/$x/$x/p/$x/j` | `{"ret":"ok","ver":"0.13.0"}` | -|


## Map Decoding Notes

- MapP topic contains an element `p` which contains a base64 encoded string
    - `p` is decoded from base64 into a byte array
    - a ByteArrayInputStream is created off of the resulting byte array
        - The first 5 bytes are placed into a variable called "props"
        - the next 4 are placed into a variable called "length"
        (to be continued)


## WIFI
 - Open ports on 192.168.0.1 (`nmap -p PORT 192.168.0.1 -Pn`)
   - 8888: `8888/tcp open  sun-answerbook`
   - 9876: `9876/tcp open  sd`
     - `nmap -p 9876 --script banner 192.168.0.1 -Pn`
       - 

## Ecovacs 900/901 Info
CPU: ARM® Cortex®-M3 - ARM GD32F103 VGT6 CE7N618 AJ1739 

### Update binaries
`portal.ecouser.net/api/ota/products/wukong/class/ls1ok3/firmware/latest.json?sn={???}&ver=X.X.X&module=fw0`

`sn` is your bots serial found under the dustbin

### Serial connection

- Remove the rubber gasket near the dustbin
- Pin 1 is TX, Pin 2 is RX @ 3.3V, Pin 6 GND
- Baud rate is `115200`
- If TX/RX are momentarily shorted, you will be presented with a ARM FIQ debugger:

```
FIQ Debugger commands:
pc PC status
regs Register dump
allregs Extended Register dump
bt Stack trace
reboot [<c>] Reboot with command <c>
reset [<c>] Hard reset with command <c>
irqs Interupt status
kmsg Kernel log
version Kernel version
last_kmsg Last kernel log
sleep Allow sleep while in FIQ
nosleep Disable sleep while in FIQ
console Switch terminal to console
cpu Current CPU
cpu <number> Switch to CPU<number>
ps Process list
sysrq sysrq options
sysrq <param> Execute sysrq with <param>
```

### Serial readout

```
Terminal ready

Audio Device:   Advanced Linux Sound Architecture (ALSA) output

Playing: /media/music/ZH/17.ogg
Ogg Vorbis stream: 1 channel, 16000 Hz
Encoder: libsndfile
killall: wget: no process killed00.90  ( 35.1 kbps)  Output Buffer  21.2% (EOS)
killall: wifi_service: no process killed
killall: wpa_supplicant: no process killed
ls: /data/autostart/*.sh: No such file or directory
/usr/bin/factory_reset.sh: line 33: /etc/rc.d/rsyslog.sh: Permission denied

Done.
[   28.040297] flash_vendor_ioctl cmd=40047602 ret = 0
The system is going down NOW!
Sent SIGTERM to all processes
[   28.084926] RTL871X: rtw_cmd_thread(wlan0) _rtw_down_sema(&pcmdpriv->cmd_queue_sema) return _FAIL, break
Sent SIGKILL to all processes
Requesting system reboot
[   30.061399] cpufreq: cpufreq: get arm regulator failed
[   30.061468] cpufreq: cpufreq: reboot set coe rate=1008000000, volt=0
[   30.065422] dwc_otg_driver_shutdown: disconnect USB device mode
r[   30.075375] rkflash_shutdown...
[   30.077854] flash th quited
[   30.088894] rkflash_shutdown:OK
[   30.090648] rk816_syscore_shutdown
[   30.192027] Rest�DDR Version V1.03 20180118 h
set vdd core 1.15v
In
600MHz
DDR3
Bus Width=16 Col=10 Bank=8 Row=13 CS=1 Die Bus-Width=16 Size=128MB
mach:2
OUT
1 BUILD: Mar  6 2018 16:46:56, version: 1.24
No.1 FLASH ID:98 f1 80 15 72 16
read_idblock 100
IdBlockReadData 0 100
read_idblock fcdc8c3b 100
boot_media = 0x1
flash vendor_storage_init
OK! 92840
loader flag: 0x5242c300
start_linux=====93482
update_id = 2
OS3 REG: 0x2
boot_mode2!!!!!! ,99 ms
update os3: 0x2
kernel_addr d400, 100 ms
load data done @460 ms
run kernel@0x62000000 =====512 ms
<hit enter to activate fiq debugger>
[    0.000000] Booting Linux on physical CPU 0xf00
[    0.000000] Initializing cgroup subsys cpuset
[    0.000000] Initializing cgroup subsys cpu
[    0.000000] Initializing cgroup subsys cpuacct
[    0.000000] Linux version 3.10.104 (xiaomujiang@xiaomujiang-MS-7788) (gcc version 5.3.1 20160412 (Linaro GCC 5.3-2016.05) ) #2 SMP PREEMPT Tue Oct 9 16:40:16 CST 2018
[    0.000000] CPU: ARMv7 Processor [410fc075] revision 5 (ARMv7), cr=10c5387d
[    0.000000] CPU: PIPT / VIPT nonaliasing data cache, VIPT aliasing instruction cache
[    0.000000] Machine: Rockchip RV1108, model: Rockchip RV1108 EVB MAINBOARD V12
[    0.000000] early_init_dt_scan_chosen: rootfs id: 0x2
[    0.000000] Command line is: user_debug=31 rockchip_jtag noinitrd root=/dev/rootfs2 rootfstype=squashfs
[    0.000000] rockchip_ion_reserve
[    0.000000] ion heap(carveout): base(62000000) size(100000) align(0)
[    0.000000] ion heap(cma): base(0) size(0) align(0)
[    0.000000] ion heap(vmalloc): base(0) size(0) align(0)
[    0.000000] ion_reserve: carveout reserved base 62000000 size 1048576
[    0.000000] cma: CMA: reserved 2 MiB at 67e00000
[    0.000000] Memory policy: ECC disabled, Data cache writealloc
[    0.000000] PERCPU: Embedded 9 pages/cpu @c09ad000 s13056 r8192 d15616 u36864
[    0.000000] Built 1 zonelists in Zone order, mobility grouping on.  Total pages: 32512
[    0.000000] Kernel command line: user_debug=31 rockchip_jtag noinitrd root=/dev/rootfs2 rootfstype=squashfs
[    0.000000] rockchip jtag enabled
[    0.000000] PID hash table entries: 512 (order: -1, 2048 bytes)
[    0.000000] Dentry cache hash table entries: 16384 (order: 4, 65536 bytes)
[    0.000000] Inode-cache hash table entries: 8192 (order: 3, 32768 bytes)
[    0.000000] allocated 262144 bytes of page_cgroup
[    0.000000] please try 'cgroup_disable=memory' option if you don't want memory cgroups
[    0.000000] Memory: 128MB = 128MB total
[    0.000000] Memory: 117604k/117604k available, 13468k reserved, 0K highmem
[    0.000000] Virtual kernel memory layout:
[    0.000000]     vector  : 0xffff0000 - 0xffff1000   (   4 kB)
[    0.000000]     fixmap  : 0xfff00000 - 0xfffe0000   ( 896 kB)
[    0.000000]     vmalloc : 0xc8800000 - 0xff000000   ( 872 MB)
[    0.000000]     lowmem  : 0xc0000000 - 0xc8000000   ( 128 MB)
[    0.000000]     pkmap   : 0xbfe00000 - 0xc0000000   (   2 MB)
[    0.000000]     modules : 0xbf000000 - 0xbfe00000   (  14 MB)
[    0.000000]       .text : 0xc0008000 - 0xc068abbc   (6667 kB)
[    0.000000]       .init : 0xc068b000 - 0xc06d3300   ( 289 kB)
[    0.000000]       .data : 0xc06d4000 - 0xc07c0728   ( 946 kB)
[    0.000000]        .bss : 0xc07c0728 - 0xc086fb60   ( 702 kB)
[    0.000000] SLUB: HWalign=64, Order=0-3, MinObjects=0, CPUs=1, Nodes=1
[    0.000000] Preemptible hierarchical RCU implementation.
[    0.000000] 	RCU dyntick-idle grace-period acceleration is enabled.
[    0.000000] 	RCU restricting CPUs from NR_CPUS=4 to nr_cpu_ids=1.
[    0.000000] NR_IRQS:16 nr_irqs:16 16
[    0.000000] GIC CPU mask not found - kernel will fail to boot.
[    0.000000] GIC CPU mask not found - kernel will fail to boot.
[    0.000000] rk_clk_tree_init start!
[    0.000000] rk_get_uboot_display_flag: uboot_logo_on = 0
[    0.000000] rkclk_init_clks: cnt_parent = 7
[    0.000000] rkclk_init_clks: cnt_rate = 28
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=uart2_frac can't get rate=464062
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=uart1_frac can't get rate=464062
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=uart0_frac can't get rate=464062
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=i2s2_frac can't get rate=464062
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=i2s1_frac can't get rate=464062
[    0.000000] rkclk: can't get a available nume and deno
[    0.000000] rkclk: clk_frac_div name=i2s0_frac can't get rate=464062
[    0.000000] Architected cp15 timer(s) running at 24.00MHz (phys).
[    0.000000] Switching to timer-based delay loop
[    0.000000] sched_clock: ARM arch timer >56 bits at 24000kHz, resolution 41ns
[    0.000000] process version: 1
[    0.000000] channel:0, lkg:6
[    0.000000] target-temp:95
[    0.000000] channel:0, lkg:6
[    0.000000] channel:0, lkg:6
[    0.000000] sched_clock: 32 bits at 1kHz, resolution 1000000ns, wraps every 4294967295ms
[    0.000000] Console: colour dummy device 80x30
[    0.000000] console [tty0] enabled
[    2.111681] Calibrating delay loop (skipped), value calculated using timer frequency.. 48.00 BogoMIPS (lpj=24000)
[    2.111738] pid_max: default: 32768 minimum: 301
[    2.112015] Mount-cache hash table entries: 512
[    2.113123] Initializing cgroup subsys memory
[    2.113271] Initializing cgroup subsys devices
[    2.113317] Initializing cgroup subsys freezer
[    2.113353] Initializing cgroup subsys blkio
[    2.113384] Initializing cgroup subsys perf_event
[    2.113493] CPU: Testing write buffer coherency: ok
[    2.113951] /cpus/cpu@0 missing clock-frequency property
[    2.114093] CPU0: thread -1, cpu 0, socket 15, mpidr 80000f00
[    2.114157] Setting up static identity map for 0xc04b2b60 - 0xc04b2bb8
[    2.115381] last_log: 0x67a80000 map to 0xc8814000 and copy to 0xc8836000, size 0x20000 early 0x1796 (version 3.1)
[    2.121854] Brought up 1 CPUs
[    2.121904] SMP: Total of 1 processors activated (48.00 BogoMIPS).
[    2.121942] CPU: All CPU(s) started in SVC mode.
[    2.123168] devtmpfs: initialized
[    2.130134] VFP support v0.3: implementor 41 architecture 2 part 30 variant 7 rev 5
[    2.132143] pinctrl core: initialized pinctrl subsystem
[    2.132691] regulator-dummy: no parameters
[    2.154156] NET: Registered protocol family 16
[    2.155523] DMA: preallocated 256 KiB pool for atomic coherent allocations
[    2.156517] ion_snapshot: 0x67b68000 map to 0xc8857000 and copy to 0xc084d8ac (version 0.1)
[    2.156743] Registered FIQ tty driver
[    2.158104] ------------[ cut here ]------------
[    2.158174] WARNING: at kernel/irq/manage.c:432 enable_irq+0x60/0x74()
[    2.158213] Unbalanced enable for IRQ 76
[    2.158241] Modules linked in:
[    2.158281] CPU: 0 PID: 1 Comm: swapper/0 Not tainted 3.10.104 #2
[    2.158343] [<c0013d9c>] (unwind_backtrace+0x0/0xdc) from [<c001136c>] (show_stack+0x10/0x14)
[    2.158399] [<c001136c>] (show_stack+0x10/0x14) from [<c0038e58>] (warn_slowpath_common+0x4c/0x6c)
[    2.158454] [<c0038e58>] (warn_slowpath_common+0x4c/0x6c) from [<c0038ea4>] (warn_slowpath_fmt+0x2c/0x3c)
[    2.158507] [<c0038ea4>] (warn_slowpath_fmt+0x2c/0x3c) from [<c009a7a0>] (enable_irq+0x60/0x74)
[    2.158564] [<c009a7a0>] (enable_irq+0x60/0x74) from [<c033c964>] (fiq_debugger_probe+0x26c/0x5e0)
[    2.158618] [<c033c964>] (fiq_debugger_probe+0x26c/0x5e0) from [<c025b494>] (driver_probe_device+0xb4/0x1f8)
[    2.158674] [<c025b494>] (driver_probe_device+0xb4/0x1f8) from [<c0259e00>] (bus_for_each_drv+0x80/0x90)
[    2.158724] [<c0259e00>] (bus_for_each_drv+0x80/0x90) from [<c025b3a0>] (device_attach+0x64/0x88)
[    2.158773] [<c025b3a0>] (device_attach+0x64/0x88) from [<c025aa04>] (bus_probe_device+0x28/0x98)
[    2.158824] [<c025aa04>] (bus_probe_device+0x28/0x98) from [<c0259314>] (device_add+0x460/0x554)
[    2.158877] [<c0259314>] (device_add+0x460/0x554) from [<c025c8b4>] (platform_device_add+0x134/0x1c0)
[    2.158931] [<c025c8b4>] (platform_device_add+0x134/0x1c0) from [<c0695054>] (rk_serial_debug_init+0x1a4/0x21c)
[    2.158986] [<c0695054>] (rk_serial_debug_init+0x1a4/0x21c) from [<c06952dc>] (rk_fiq_debugger_init+0x210/0x254)
[    2.159039] [<c06952dc>] (rk_fiq_debugger_init+0x210/0x254) from [<c00086cc>] (do_one_initcall+0x8c/0x128)
[    2.159092] [<c00086cc>] (do_one_initcall+0x8c/0x128) from [<c068bbcc>] (kernel_init_freeable+0x15c/0x218)
[    2.159146] [<c068bbcc>] (kernel_init_freeable+0x15c/0x218) from [<c04a5dd4>] (kernel_init+0x8/0xe0)
[    2.159203] [<c04a5dd4>] (kernel_init+0x8/0xe0) from [<c000da20>] (ret_from_fork+0x14/0x34)
[    2.159283] ---[ end trace 1b75b31a2719ed1c ]---
[    2.160462] console [ttyFIQ0] enabled
[    2.160900] Registered fiq debugger ttyFIQ0
[    2.172735] syscon 10300000.syscon: regmap [mem 0x10300000-0x10300fff] registered
[    2.173184] syscon 20060000.syscon: regmap [mem 0x20060000-0x20060fff] registered
[    2.173656] syscon 202a0000.syscon: regmap [mem 0x202a0000-0x202a0fff] registered
[    2.179037] syscon 20200000.syscon: regmap [mem 0x20200000-0x20200fff] registered
[    2.197368] hw-breakpoint: found 5 (+1 reserved) breakpoint and 4 watchpoint registers.
[    2.197439] hw-breakpoint: maximum watchpoint size is 8 bytes.
[    2.198521] rk3368_init_rockchip_pmu_ops: could not find pmu dt node
[    2.255428] bio: create slab <bio-0> at 0
[    2.261000] SCSI subsystem initialized
[    2.261394] usbcore: registered new interface driver usbfs
[    2.261523] usbcore: registered new interface driver hub
[    2.261887] usbcore: registered new device driver usb
[    2.263429] rv1108-dwc-control-usb dwc-control-usb.10: no hclk_usb_peri clk specified
[    2.269565] rk3x-i2c 20000000.i2c: Initialized RK3xxx I2C bus at c8870000
[    2.270169] RK29 Watchdog Timer, (c) 2011 Rockchip Electronics
[    2.271673] ion_carveout_heap_create: 100000@62000000
[    2.276197] Rockchip ion module is successfully loaded (v1.1)
[    2.281405] Advanced Linux Sound Architecture Driver Initialized.
[    2.282720] cfg80211: Calling CRDA to update world regulatory domain
[    2.284452] rk816_i2c_probe,line=785
[    2.284991] pmic is rk805, chip version is 8050
[    2.285398] pmic on/off source: on=0x40, off=0x0
[    2.290638] rk816_i2c_probe: rk816_pmic_sleep=0
[    2.295712] rk816_regulator_probe: compatible rk805
[    2.298703] vdd_core: 712 <--> 1450 mV at 1150 mV
[    2.302087] vcc_22: 2200 mV
[    2.304683] vcc_ddr: 1200 mV
[    2.308010] vcc_33: 3300 mV
[    2.310954] vdd_10: 1000 mV
[    2.313942] vcc_18: 1800 mV
[    2.316886] vdd10_pmu: 1000 mV
[    2.317701] rk816_i2c_probe success
[    2.317794] i2c-core: driver [rk816] using legacy suspend method
[    2.317837] i2c-core: driver [rk816] using legacy resume method
[    2.317984] rk816_rtc_probe,line=463
[    2.327322] rk8xx-rtc rk8xx-rtc: rtc core: registered rk816 as rtc0
[    2.327836] rk805: rtc ok
[    2.328198] rockchip-i2s 10120000.i2s1: i2s1 has no mclk
[    2.329249] Switching to clocksource arch_sys_counter
[    2.428053] NET: Registered protocol family 2
[    2.429359] TCP established hash table entries: 1024 (order: 1, 8192 bytes)
[    2.429457] TCP bind hash table entries: 1024 (order: 1, 8192 bytes)
[    2.429525] TCP: Hash tables configured (established 1024 bind 1024)
[    2.429628] TCP: reno registered
[    2.429678] UDP hash table entries: 256 (order: 1, 8192 bytes)
[    2.429809] UDP-Lite hash table entries: 256 (order: 1, 8192 bytes)
[    2.430208] NET: Registered protocol family 1
[    2.430423] rk816_gpio_probe: compatible rk805
[    2.430685] rk8xx-gpio rk8xx-gpio: driver success
[    2.431335] hw perfevents: enabled with ARMv7_Cortex_A7 PMU driver, 5 counters available
[    2.432114] rknandbase v1.0 2014-03-31
[    2.432395] rknand_driver:ret = 0
[    2.457593] squashfs: version 4.0 (2009/01/31) Phillip Lougher
[    2.457948] msgmni has been set to 233
[    2.480105] io scheduler noop registered
[    2.480156] io scheduler deadline registered
[    2.480451] io scheduler cfq registered (default)
[    2.484988] dma-pl330 102a0000.pdma: Loaded driver for PL330 DMAC-2364208
[    2.485058] dma-pl330 102a0000.pdma: 	DBUFF-128x8bytes Num_Chans-8 Num_Peri-20 Num_Events-16
[    2.485717] rk_serial.c v2.0 2017-06-30
[    2.489244] serial 10210000.serial: dma_rx_buffer c7e00000
[    2.489312] serial 10210000.serial: dma_rx_phy 0x67e00000
[    2.489408] serial 10210000.serial: serial_rk_init_dma_rx sucess
[    2.489468] serial 10210000.serial: dma_tx_buffer c7e04000
[    2.489504] serial 10210000.serial: dma_tx_phy 0x67e04000
[    2.489571] serial 10210000.serial: serial_rk_init_dma_tx success
[    2.489630] 10210000.serial: ttyS2 at MMIO 0x10210000 (irq = 78) is a rk29_serial.2
[    2.490255] serial 10210000.serial: membase fed60000
[    2.490838] serial 10220000.serial: dma_rx_buffer c7e08000
[    2.490904] serial 10220000.serial: dma_rx_phy 0x67e08000
[    2.491005] serial 10220000.serial: serial_rk_init_dma_rx sucess
[    2.491069] serial 10220000.serial: dma_tx_buffer c7e05000
[    2.491110] serial 10220000.serial: dma_tx_phy 0x67e05000
[    2.491172] serial 10220000.serial: serial_rk_init_dma_tx success
[    2.491228] 10220000.serial: ttyS1 at MMIO 0x10220000 (irq = 77) is a rk29_serial.1
[    2.491808] serial 10220000.serial: membase c887e000
[    2.513600] brd: module loaded
[    2.526982] loop: module loaded
[    2.527954] zram: Created 1 device(s)
[    2.529470] pegasus: v0.9.3 (2013/04/25), Pegasus/Pegasus II USB Ethernet driver
[    2.529632] usbcore: registered new interface driver pegasus
[    2.529755] usbcore: registered new interface driver rtl8150
[    2.529851] usbcore: registered new interface driver r8152
[    2.529971] usbcore: registered new interface driver asix
[    2.530064] usbcore: registered new interface driver ax88179_178a
[    2.530232] usbcore: registered new interface driver cdc_ether
[    2.530356] usbcore: registered new interface driver dm9601
[    2.530454] usbcore: registered new interface driver dm9620
[    2.530567] usbcore: registered new interface driver smsc75xx
[    2.530707] usbcore: registered new interface driver smsc95xx
[    2.530799] usbcore: registered new interface driver gl620a
[    2.530884] usbcore: registered new interface driver net1080
[    2.530989] usbcore: registered new interface driver plusb
[    2.531065] ehci_hcd: USB 2.0 'Enhanced' Host Controller (EHCI) Driver
[    2.535028] ehci-platform: EHCI generic platform driver
[    2.535426] ehci-platform 30140000.usb: EHCI Host Controller
[    2.535517] ehci-platform 30140000.usb: new USB bus registered, assigned bus number 1
[    2.536175] ehci-platform 30140000.usb: irq 47, io mem 0x30140000
[    2.541976] ehci-platform 30140000.usb: USB 2.0 started, EHCI 1.00
[    2.542162] usb usb1: New USB device found, idVendor=1d6b, idProduct=0002
[    2.542217] usb usb1: New USB device strings: Mfr=3, Product=2, SerialNumber=1
[    2.542262] usb usb1: Product: EHCI Host Controller
[    2.542299] usb usb1: Manufacturer: Linux 3.10.104 ehci_hcd
[    2.542336] usb usb1: SerialNumber: 30140000.usb
[    2.543324] hub 1-0:1.0: USB hub found
[    2.543405] hub 1-0:1.0: 1 port detected
[    2.544032] ohci_hcd: USB 1.1 'Open' Host Controller (OHCI) Driver
[    2.544090] ohci-platform: OHCI generic platform driver
[    2.544451] ohci-platform 30160000.usb: Generic Platform OHCI controller
[    2.544561] ohci-platform 30160000.usb: new USB bus registered, assigned bus number 2
[    2.544686] ohci-platform 30160000.usb: irq 48, io mem 0x30160000
[    2.602051] usb usb2: New USB device found, idVendor=1d6b, idProduct=0001
[    2.602117] usb usb2: New USB device strings: Mfr=3, Product=2, SerialNumber=1
[    2.602165] usb usb2: Product: Generic Platform OHCI controller
[    2.602206] usb usb2: Manufacturer: Linux 3.10.104 ohci_hcd
[    2.602241] usb usb2: SerialNumber: 30160000.usb
[    2.603208] hub 2-0:1.0: USB hub founStarting UDev Daemon
d
[    2.603288] hub 2-0:1.0: 1 port detected
[    2.604087] usbcore: registered new interface driver usb-storage
[    2.604346] usbcore: registered new interface driver usbserial
[    2.604454] usbcore: registered new interface driver usbserial_generic
[    2.604550] usbserial: USB Serial support registered for generic
[    2.604654] usbcore: registered new interface driver ftdi_sio
[    2.604737] usbserial: USB Serial support registered for FTDI USB Serial Device
[    2.605077] usbcore: registered new interface driver pl2303
[    2.605188] usbserial: USB Serial support registered for pl2303
[    2.605241] usb20_otg: version 3.10a 21-DEC-2012
[    2.608997] c8c80040
[    2.609050] Core Release: 3.10a
[    2.609086] Setting default values for core params
[    2.609352] Using Buffer DMA mode
[    2.609401] Periodic Transfer Interrupt Enhancement - disabled
[    2.609438] Multiprocessor Interrupt Enhancement - disabled
[    2.609475] OTG VER PARAM: 0, OTG VER FLAG: 0
[    2.609505] ^^^^^^^^^^^^^^^^^Device Mode
[    2.609544] Dedicated Tx FIFOs mode
[    2.609592] pcd_init otg_dev = c76eae40
[    2.610038] usb20_otg 30180000.usb: DWC OTG Controller
[    2.610131] usb20_otg 30180000.usb: new USB bus registered, assigned bus number 3
[    2.610229] usb20_otg 30180000.usb: irq 50, io mem 0x00000000
[    2.610382] usb usb3: New USB device found, idVendor=1d6b, idProduct=0002
[    2.610438] usb usTriggering UDev uevents
b3: New USB device strings: Mfr=3, Product=2, SerialNumber=1
[    2.610484] usb usb3: Product: DWC OTG Controller
[    2.610518] usb usb3: Manufacturer: Linux 3.10.104 dwc_otg_hcd
[    2.610555] usb usb3: SerialNumber: 30180000.usb
[    2.611601] hub 3-0:1.0: USB hub found
[    2.611677] hub 3-0:1.0: 1 port detected
[    2.613343] usb20_host: version 3.10a 21-DEC-2012
[    2.619427] input: rk816_pwrkey as /devices/20000000.i2c/i2c-0/0-0018/rk8xx-pwrkey/input/input0
[    2.619926] rk805 register rk8xx_pwrkey driver
[    2.620029] i2c /dev entries driver
[    2.627220] rockchip_temp_probe,line=384
[    2.630883] tsadc 10370000.tsadc: Missing tsadc_low_temp in the DT.
[    2.631118] tsadc 10370000.tsadc: initialized
[    2.633440] cpufreq version 1.0, suspend freq 1008 MHz
[    2.633915] cpuidle: using governor ladder
[    2.633967] cpuidle: using governor menu
[    2.634710] rknandc_base v1.1 2017-01-11
[    2.635080] rknaudevd[71]: specified group 'dialout' unknown

ndc 30100000.nandc: rknandc_probe clk rate = 148500000
[    2.635172] rkflash_dev_init
[    2.635204] init rkfudevd[71]: specified group 'cdromlash[0]
[    2.635255] No.1 FLASH ID:98 f1 80 1' unknown

udevd[71]: speci5 72 16
[    2.635300] SFTL version: 5.0.48 20181009
[    2.711269] dwc_otg_hcd_susfied group 'tape' unknown

pend, usb device mode
[    2.762586] ...FtlVpcCheckAndModify enter...
[    2.788101] FtlGcRefreshBlock  0x153
[    2.788148] FtlGcRefreshBlock  0x10b
[    2.819163] rkflash[0] init success
[    2.823656]    sysinfo: 0x000005000 -- 0x000008000 (0 MB)
[    2.824464]    IDBlock: 0x000008000 -- 0x000078000 (0 MB)
[    2.824994]    kernel1: 0x000080000 -- 0x000680000 (6 MB)
[    2.827596]    rootfs1: 0x000680000 -- 0x001a80000 (20 MB)
[    2.828111]    kernel2: 0x001a80000 -- 0x002080000 (6 MB)
[    2.828686]    rootfs2: 0x002080000 -- 0x003480000 (20 MB)
[    2.829142]     datafs: 0x003480000 -- 0x005280000 (30 MB)
[    2.868007] rockchip-rv1108 rockchip_audio.15:  rv1108-hifi <-> 10120000.i2s1 mapping ok
[    2.872982] u32 classifier
[    2.873023]     Actions configured
[    2.873224] TCP: cubic registered
[    2.873258] Initializing XFRM netlink socket
[    2.873303] NET: Registered protocol family 17
[    2.873372] NET: Registered protocol family 15
[    2.873605] l2tp_core: L2TP core driver, V2.0
[    2.874086] rv1108_suspend_init: pm_ctrbits = 0x100166
[    2.874417] clks_gating_suspend_init:init ok
[    2.874479] Registering SWP/SWPB emulation handler
[    2.875270] flash vendor_init_thread!
[    2.879518] ddrfreq: verion 1.2 20140526
[    2.879570] ddrfreq: normal 600MHz performance 792MHz low_power 396MHz video_1080p 0MHz video_4k 0MHz dualview 0MHz idle 0MHz suspend 0MHz reboot 600MHz video_4k_10b 0MHz
[    2.879626] ddrfreq: auto-freq=0
[    2.879647] ddrfreq: auto-freq-table epmty!
[    2.879975] flash vendor storage:20170308 ret = 0
[    2.881139] regulator-dummy: disabling
[    2.881742] pcd_pullup, is_on 0
[    2.881878] file system registered
[    2.886288] android_usb gadget: Mass Storage Function, version: 2009/09/11
[    2.886410] android_usb gadget: Number of LUNs=2
[    2.886448]  lun0: LUN: removable file: (no medium)
[    2.886476]  lun1: LUN: removable file: (no medium)
[    2.887090] android_usb gadget: android_usb ready
[    2.888491] rk8xx-rtc rk8xx-rtc: setting system clock to 2011-01-04 11:47:07 UTC (1294141627)
[    2.906840] ALSA device list:
[    2.906894]   #0: RK_RV1108
[    2.936204] VFS: Mounted root (squashfs filesystem) readonly on device 31:5.
[    2.994091] devtmpfs: mounted
[    2.994824] Freeing unused kernel memory: 288K (c068b000 - c06d3000)
[    3.741608] udevd[71]: starting version 182
Starting Daemons
ext2fs_check_if_mount: Can't check if filesystem is mounted due to missing mtab file while determining whether /dev/datafs is mounted.
[    4.570081] EXT4-fs (datafs): couldn't mount as ext3 due to feature incompatibilities
[    4.576125] EXT4-fs (datafs): couldn't mount as ext2 due to feature incompatibilities
[    4.592773] EXT4-fs (datafs): mounted filesystem with ordered data mode. Opts: (null)
sh: write error: Device or resource busy
/etc/rc.d/sysconfig.sh: line 61: can't create /sys/class/gpio/gpio11/direction: nonexistent directory
/etc/rc.d/sysconfig.sh: line 61: can't create /sys/class/gpio/gpio11/value: nonexistent directory
1+0 records in
1+0 records out
512 bytes (512B) copied, 0.001272 seconds, 393.1KB/s
[    5.357233] usb 1-1: new high-speed USB device number 2 using ehci-platform

Audio Device:   Advanced Linux Sound Architecture (ALSA) output

Playing: /media/music/ZH/0.ogg
Ogg Vorbis stream: 1 channel, 16000 Hz
Encoder: libsndfile
[    5.472366] usb 1-1: New USB device found, idVendor=0bda, idProduct=f179
[    5.472437] usb 1-1: New USB device strings: Mfr=1, Product=2, SerialNumber=3
[    5.472476] usb 1-1: Product: 802.11n
[    5.472505] usb 1-1: Manufacturer: Realtek
[    5.472532] usb 1-1: SerialNumber: 805E4F9CD54C
[    5.565916] [otg id chg] last id -1 current id 8192utput Buffer  79.0% (EOS)
[    5.566072] PortPower off
[    5.669349] Using Buffer DMA mode
[    5.669414] Periodic Transfer Interrupt Enhancement - disabled
[    5.669448] Multiprocessor Interrupt Enhancement - disabled
[    5.669479] OTG VER PARAM: 0, OTG VER FLAG: 0
[    5.669506] ^^^^^^^^^^^^^^^^^Device Mode
[    5.673018] dwc_otg_hcd_resume, usb device mode
[    5.772849] dwc_otg_hcd_suspend, usb device mode
[    6.162205] ***************vbus detect*****************
[    6.559309] RTL871X: module init start
[    6.559385] RTL871X: rtl8188fu v4.3.23.6_20964.20170110
[    6.559418] RTL871X: build time: Oct 16 2017 15:02:23
[    6.699778] RTL871X: hal_com_config_channel_plan chplan:0x20
[    6.791473] RTL871X: rtw_ndev_init(wlan0) if1 mac_addr=80:5e:4f:9c:d5:4c
[    6.806843] RTL871X: rtw_ndev_init(wlan1) if2 mac_addr=82:5e:4f:9c:d5:4c
[    6.830458] usbcore: registered new interface driver rtl8188fu
[    6.830532] RTL871X: module init ret=0
ls: /data/autostart/*.sh: No such file or directory  Output Buffer  34.5% (EOS)


Done.
[   10.397816] serial 10220000.serial: error:lsr=0xc0
+ date -s 2011-1-4 11:47:17
+ hwclock -w
+ [ _0 == _0 ]
+ date
+ echo Tue Jan 4 11:47:17 UTC 2011 set time success!!!
[   11.268970] RTL871X: nolinked power save enter
killall: kiss: no process killed
killall: wifi_service: no process killed
killall: dhcpcd: no process killed
killall: dnsmasq: no process killed
killall: hostapd: no process killed
[   13.473695] RTL871X: module exit start
[   13.473764] usbcore: deregistering interface driver rtl8188fu
[   13.480272] RTL871X: rtw_ndev_uninit(wlan0) if1
[   13.495153] RTL871X: rtw_ndev_uninit(wlan1) if2
[   13.504034] RTL871X: rtw_cmd_thread: DriverStopped(True) SurpriseRemoved(False) break at line 581
[   13.504381] RTL871X: rtw_dev_unload: driver in IPS-FWLPS
[   13.608378] usb 1-1: reset high-speed USB device number 2 using ehci-platform
[   13.753748] RTL871X: module exit success
[   14.885110] RTL871X: module init start
[   14.885184] RTL871X: rtl8188fu v4.3.23.6_20964.20170110
[   14.890053] RTL871X: build time: Oct 16 2017 15:02:23
[   15.042972] RTL871X: hal_com_config_channel_plan chplan:0x20
[   15.147541] RTL871X: rtw_ndev_init(wlan0) if1 mac_addr=80:5e:4f:9c:d5:4c
[   15.160048] RTL871X: rtw_ndev_init(wlan1) if2 mac_addr=82:5e:4f:9c:d5:4c
[   15.178354] usbcore: registered new interface driver rtl8188fu
[   15.180652] RTL871X: module init ret=0
[   18.064504] hrtimer: interrupt took 35291 ns
[   18.189878] RTL871X: assoc success

deboot login:
```

### NMAP report
```
Nmap scan report for 192.168.0.1
Host is up (0.0070s latency).
Not shown: 997 filtered tcp ports (no-response)
PORT     STATE SERVICE         VERSION
53/tcp   open  domain          dnsmasq UNKNOWN
8888/tcp open  sun-answerbook?
9876/tcp open  sd?
1 service unrecognized despite returning data. If you know the service/version, please submit the following fingerprint at https://nmap.org/cgi-bin/submit.cgi?new-service :
SF-Port8888-TCP:V=7.94%I=7%D=1/17%Time=65A886C3%P=arm-apple-darwin22.6.0%r
SF:(GetRequest,79,"HTTP/1\.1\x20400\x20Bad\x20Request\r\nServer:\x20Medusa
SF:\x20Bumbee\r\nConnection:\x20close\r\nContent-Type:\x20application/json
SF:\r\nContent-Length:\x200\r\n\r\n")%r(HTTPOptions,79,"HTTP/1\.1\x20400\x
SF:20Bad\x20Request\r\nServer:\x20Medusa\x20Bumbee\r\nConnection:\x20close
SF:\r\nContent-Type:\x20application/json\r\nContent-Length:\x200\r\n\r\n")
SF:%r(FourOhFourRequest,79,"HTTP/1\.1\x20400\x20Bad\x20Request\r\nServer:\
SF:x20Medusa\x20Bumbee\r\nConnection:\x20close\r\nContent-Type:\x20applica
SF:tion/json\r\nContent-Length:\x200\r\n\r\n")%r(GenericLines,79,"HTTP/1\.
SF:1\x20400\x20Bad\x20Request\r\nServer:\x20Medusa\x20Bumbee\r\nConnection
SF::\x20close\r\nContent-Type:\x20application/json\r\nContent-Length:\x200
SF:\r\n\r\n")%r(RTSPRequest,79,"HTTP/1\.1\x20400\x20Bad\x20Request\r\nServ
SF:er:\x20Medusa\x20Bumbee\r\nConnection:\x20close\r\nContent-Type:\x20app
SF:lication/json\r\nContent-Length:\x200\r\n\r\n")%r(SIPOptions,79,"HTTP/1
SF:\.1\x20400\x20Bad\x20Request\r\nServer:\x20Medusa\x20Bumbee\r\nConnecti
SF:on:\x20close\r\nContent-Type:\x20application/json\r\nContent-Length:\x2
SF:00\r\n\r\n");
```

