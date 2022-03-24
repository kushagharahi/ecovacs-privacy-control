package main

import (
	"fmt"
	"strconv"
	"strings"

	mxj "github.com/clbanning/mxj/v2"
)

func handleResponse(cmdName string, msgValues mxj.Map) {
	switch cmdName {
	case "GetMapM":
		fmt.Printf("%s %s\n", cmdName, msgValues)
		getMapDataValues(&msgValues)
	}
}

type Position struct {
	angle int
	x     int
	y     int
}

type MapInfo struct {
	boxBottomRight Position
	boxTopLeft     Position
	columnGrid     int
	columnPiece    int
	crc            []int64
	mapId          int
	pixelWidth     int
	rowGrid        int
	rowPiece       int
	buffer         [][]byte
}

func getMapDataValues(msgValues *mxj.Map) (*MapInfo, error) {
	values, err := msgValues.ValueForKey("ctl")
	if err != nil {
		return nil, fmt.Errorf("got error getting ctl values %w", err)
	}
	valuesMap := values.(map[string]interface{})
	var mapInfo MapInfo

	mapInfo.mapId, _ = strconv.Atoi(valuesMap["-i"].(string))
	mapInfo.columnGrid, _ = strconv.Atoi(valuesMap["-w"].(string))
	mapInfo.rowGrid, _ = strconv.Atoi(valuesMap["-h"].(string))
	mapInfo.columnPiece, _ = strconv.Atoi(valuesMap["-c"].(string))
	mapInfo.rowPiece, _ = strconv.Atoi(valuesMap["-r"].(string))
	mapInfo.pixelWidth, _ = strconv.Atoi(valuesMap["-p"].(string))
	mapInfo.crc, _ = sliceInt64(strings.Split(valuesMap["-m"].(string), ","))
	// TODO: BOX

	// zeroCrc := crc32.ChecksumIEEE(make([]byte, (mapInfo.rowGrid * mapInfo.columnGrid)))
	// for i := 0; i < len(mapInfo.crc); i++ {
	// 	if mapInfo.crc[i] == int64(zeroCrc) {
	// 		for p := 0; p < len
	// 	}
	// }

	numMapPieces := mapInfo.columnPiece * mapInfo.rowPiece

	for i := 0; i < numMapPieces; i++ {
		publishXML(PullMp(i))
	}

	return &mapInfo, nil
}

func sliceInt64(sa []string) ([]int64, error) {
	si := make([]int64, 0, len(sa))
	for _, a := range sa {
		i, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			return si, err
		}
		si = append(si, i)
	}
	return si, nil
}
