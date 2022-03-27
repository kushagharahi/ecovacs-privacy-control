package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	mxj "github.com/clbanning/mxj/v2"
	"github.com/kjk/lzma"
)

func handleResponse(cmdName string, msgValues mxj.Map) {
	fmt.Printf("%s %s\n", cmdName, msgValues)

	values, err := msgValues.ValueForKey("ctl")

	if err != nil {
		err = fmt.Errorf("got error getting ctl values %w", err)
		fmt.Println(err)
		//todo bubble up errors
	}
	if values != nil {
		valuesMap := values.(map[string]interface{})

		switch cmdName {
		case "GetMapM":
			mapInfo, _ = getMapDataValues(valuesMap)
		case "MapP":
			updateMapBuffer(valuesMap)
		case "PullMP":
			updateMapBuffer(valuesMap)
		}
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

//valuesMap is not a pointer because map values are passed by reference
func getMapDataValues(valuesMap map[string]interface{}) (MapInfo, error) {

	var mapInfo MapInfo

	mapInfo.mapId, _ = strconv.Atoi(valuesMap["-i"].(string))
	mapInfo.columnGrid, _ = strconv.Atoi(valuesMap["-w"].(string))
	mapInfo.rowGrid, _ = strconv.Atoi(valuesMap["-h"].(string))
	mapInfo.columnPiece, _ = strconv.Atoi(valuesMap["-c"].(string))
	mapInfo.rowPiece, _ = strconv.Atoi(valuesMap["-r"].(string))
	mapInfo.pixelWidth, _ = strconv.Atoi(valuesMap["-p"].(string))
	mapInfo.crc, _ = sliceInt64(strings.Split(valuesMap["-m"].(string), ","))
	mapInfo.buffer = make([][]byte, mapInfo.rowGrid*mapInfo.rowPiece, mapInfo.columnGrid*mapInfo.columnPiece)
	// TODO: BOX

	numMapPieces := mapInfo.columnPiece * mapInfo.rowPiece

	for i := 0; i < numMapPieces; i++ {
		publishXML(PullMp(i))
	}

	return mapInfo, nil
}

func updateMapBuffer(valuesMap map[string]interface{}) {
	if base64EncodedString, ok := valuesMap["-p"].(string); ok {
		//lzma is 7z compression
		lmzaCompressedMapPieceByteArr, err := base64.StdEncoding.DecodeString(base64EncodedString)

		if err != nil {
			//TODO bubble up errors
			err = fmt.Errorf("Could not decode map piece from base64 %w", err)
			fmt.Println(err)
			return
		}

		//insert missing 4 bytes at index 8
		//In the lzma header the length parameter is not correct (lzma format: https://svn.python.org/projects/external/xz-5.0.3/doc/lzma-file-format.txt)
		//The header needs to be 13 bytes instead of the 9 bytes provided
		lmzaCompressedMapPieceByteArr = append(lmzaCompressedMapPieceByteArr[:12], lmzaCompressedMapPieceByteArr[8:]...)
		lmzaCompressedBuffer := bytes.NewBuffer(lmzaCompressedMapPieceByteArr)

		decodedMapPieceByteArr := make([]byte, 10000)
		lzmaReadCloser := lzma.NewReader(lmzaCompressedBuffer)

		lzmaReadCloser.Read(decodedMapPieceByteArr)
		lzmaReadCloser.Close()

		if err != nil {
			//TODO Bubble up errors
			err = fmt.Errorf("Error decoding lzma encoded map piece %w", err)
			fmt.Println(err)
			return
		}

		pieceData = append(pieceData, decodedMapPieceByteArr...)
		pieceIndex++

		if pieceIndex+1 == mapInfo.columnPiece*mapInfo.rowPiece {
			buildBuffer()
		}
	}

}

func buildBuffer() {
	rowStart := pieceIndex / mapInfo.rowPiece
	columnStart := pieceIndex / mapInfo.columnPiece
	for row := 0; row < mapInfo.rowGrid; row++ {
		for column := 0; column < mapInfo.columnGrid; column++ {
			bufferRow := row + rowStart*mapInfo.rowGrid
			bufferColumn := column + columnStart*mapInfo.columnGrid
			pieceDataPosition := mapInfo.rowGrid*row + column

			mapInfo.buffer[bufferRow][bufferColumn] = pieceData[pieceDataPosition]
		}
	}
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

var pieceIndex = -1
var pieceData []byte = nil
var mapInfo = MapInfo{}
