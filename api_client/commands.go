package main

import mxj "github.com/clbanning/mxj/v2"

type Msg struct {
	cmdName string
	cmdOpts map[string]string
}

type XMLMsg struct {
	cmdName string
	cmdOpts mxj.Map
}

var GetWkVer Msg = Msg{
	cmdName: "GetWKVer",
	cmdOpts: map[string]string{},
}

var GetBrushLifeSpan XMLMsg = XMLMsg{
	cmdName: "GetLifeSpan",
	cmdOpts: mxj.Map{"-type": "Brush"},
}

var PullM XMLMsg = XMLMsg{
	cmdName: "PullM",
	cmdOpts: mxj.Map{"-tp": "sa", "-msid": "0", "-mid": "0"},
}

//Request Spot Areas from robot
var GetMapSet XMLMsg = XMLMsg{
	cmdName: "GetMapSet",
	cmdOpts: mxj.Map{"-tp": "sa"},
}

var GetMapM XMLMsg = XMLMsg{
	cmdName: "GetMapM",
	cmdOpts: mxj.Map{},
}

func PullMp(pieceId int) XMLMsg {
	return XMLMsg{
		cmdName: "PullMP",
		cmdOpts: mxj.Map{"-pid": pieceId},
	}
}

// var PullMp XMLMsg = XMLMsg{
// 	cmdName: "PullMP",
// 	cmdOpts: mxj.Map{"-pid": "1"},
// }

type Sound string

const (
	BOOT_UP Sound = "0"

	// 4 Please check the driving wheels
	// 6 Please install dust bin
	// 5 Please help me out
	// 3 i am suspended
	// 17 ding
	// 18 my battery is low
	// 29 please power me on before charging
	// 30 i am here
	// 31 brush is tangled please clean my brush
	// 35 please clean my anti drop sensors
	// 48 brush is tangled please clean my brush
	// 55 I am relocating
	// 56 upgrade succeeded
	// 63 i am returning to the charging dock
	// 65 cleaning paused
	// 69 connected please go back to the ecovacs
	// i am restoring the map please don't stand beside me
	// 73 my battery is low returning to the charging dock
	// 74 difficult to locate i am starting a new cleaning cycle
	// 75 i am resuming the clean
	// 76 upgrade failed please try again
	// 77 please place me on the charging dock
	// 79 resume the clean
	// 80 i am starting the clean
	// 81 i am starting the clean
	// 82 i am starting the clean
	// 84 i am ready for mopping
	// 85 please remove the mopping plate i am building the map
	// 86 cleaning is complete returning to the charging dock
	// 89 LDS malfunction please try to tap the LDS
	// 90 I am upgrading please wait
)

func PlaySound(sound Sound) XMLMsg {
	return XMLMsg{
		cmdName: "PlaySound",
		cmdOpts: mxj.Map{"-sid": sound},
	}
}

func PlaySoundInt(num int) XMLMsg {
	return XMLMsg{
		cmdName: "PlaySound",
		cmdOpts: mxj.Map{"-sid": num},
	}
}
