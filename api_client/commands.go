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
var PullM1 XMLMsg = XMLMsg{
	cmdName: "PullM",
	cmdOpts: mxj.Map{"-tp": "sa", "-msid": "0", "-mid": "1"},
}
var PullM2 XMLMsg = XMLMsg{
	cmdName: "PullM",
	cmdOpts: mxj.Map{"-tp": "sa", "-msid": "0", "-mid": "2"},
}
var PullM3 XMLMsg = XMLMsg{
	cmdName: "PullM",
	cmdOpts: mxj.Map{"-tp": "sa", "-msid": "0", "-mid": "3"},
}
var PullM4 XMLMsg = XMLMsg{
	cmdName: "PullM",
	cmdOpts: mxj.Map{"-tp": "sa", "-msid": "0", "-mid": "10"},
}
