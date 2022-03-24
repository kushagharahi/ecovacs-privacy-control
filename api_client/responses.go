package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	mxj "github.com/clbanning/mxj/v2"
	"github.com/ulikunitz/xz"
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
			getMapDataValues(valuesMap)
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
func getMapDataValues(valuesMap map[string]interface{}) (*MapInfo, error) {

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

func updateMapBuffer(valuesMap map[string]interface{}) {
	if base64EncodedString, ok := valuesMap["-p"].(string); ok {
		fmt.Println(base64EncodedString)
		//lzma is 7z compression
		lmzaCompressedByteArr, err := base64.StdEncoding.DecodeString(base64EncodedString)
		if err != nil {
			//TODO bubble up errors
			err = fmt.Errorf("Could not decode map piece from base64 %w", err)
			fmt.Println(err)
			return
		}

		lzmaCompressedByteBuffer := bytes.NewBuffer(lmzaCompressedByteArr)

		//read first 5 bytes of lmzaCompressedByteArr
		//this will equal [93 0 0 4]
		//TODO Check these? App checks them.
		lzmaDecoderProperties := make([]byte, 5)
		lzmaCompressedByteBuffer.Read(lzmaDecoderProperties)
		fmt.Println(lzmaDecoderProperties)

		//read next four bytes; should represent an integer value to define the amount of bits used to represent the map data. It should be the same value as the amount determined by MapInfo.rowGrid * MapInfo.columnGrid
		//TODO: Check assumption
		numDataBitsByteArr := make([]byte, 4)
		lzmaCompressedByteBuffer.Read(numDataBitsByteArr)
		fmt.Println(numDataBitsByteArr)
		numDataBitsInt := int(binary.LittleEndian.Uint32(numDataBitsByteArr))
		fmt.Println(numDataBitsInt)

		if err != nil {
			//TODO Bubble up errors
			err = fmt.Errorf("Error decoding number of data bits from []byte to int %w", err)
			fmt.Println(err)
			return
		}

		lzmaMapPieceByteArr := make([]byte, lzmaCompressedByteBuffer.Len())
		lzmaCompressedByteBuffer.Read(lzmaMapPieceByteArr)

		decodedMapPieceByteArr := make([]byte, numDataBitsInt)
		lzmaReaderBuffer, err := xz.NewReader(lzmaCompressedByteBuffer)
		if numDataBitsInt != 10000 {
			lzmaReaderBuffer.Read(decodedMapPieceByteArr)
		}

		if err != nil {
			//TODO Bubble up errors
			err = fmt.Errorf("Error decoding lzma encoded map piece %w", err)
			fmt.Println(err)
			return
		}

		fmt.Println()
		fmt.Println(decodedMapPieceByteArr)

	}

}

func read_int32(data []byte) (ret int) {
	buf := bytes.NewBuffer(data)
	binary.Read(buf, binary.BigEndian, &ret)
	return
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
