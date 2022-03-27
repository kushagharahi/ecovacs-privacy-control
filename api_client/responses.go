package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
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
			pieceIndex++
			updateMapBuffer(valuesMap)
		}
	}
}

type MapInfo struct {
	columnGrid  int
	columnPiece int
	crc         []int64
	mapId       int
	pixelWidth  int
	rowGrid     int
	rowPiece    int
	mapGrid     [][]byte
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

	rowBits := mapInfo.rowGrid * mapInfo.rowPiece
	columnBits := mapInfo.columnGrid * mapInfo.columnPiece

	mapInfo.mapGrid = make([][]byte, rowBits) // Make the outer slice and give it size rowBits
	for i := 0; i < rowBits; i++ {
		mapInfo.mapGrid[i] = make([]byte, columnBits) // Make one inner slice per iteration and give it size 10
		for j := 0; j < columnBits; j++ {
			mapInfo.mapGrid[i][j] = 0
		}
	}

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

		updateBuffer(pieceIndex, decodedMapPieceByteArr)
		if pieceIndex+1 == 64 {

		}
	}

}

func updateBuffer(pieceId int, decodedMapPieceData []byte) {
	rowStart := pieceId / mapInfo.rowPiece
	columnStart := pieceId % mapInfo.columnPiece

	for row := 0; row < mapInfo.rowGrid; row++ {
		for column := 0; column < mapInfo.columnGrid; column++ {

			bufferRow := row + rowStart*mapInfo.rowGrid
			bufferColumn := column + columnStart*mapInfo.columnGrid
			pieceDataPosition := mapInfo.rowGrid*row + column

			mapInfo.mapGrid[bufferRow][bufferColumn] = decodedMapPieceData[pieceDataPosition]
		}
	}
}

func getImageFromMapGrid() image.Image {
	scaledImg := getScaledMapGrid()
	scaledImgX := len(scaledImg)
	scaledImgY := len(scaledImg[0])
	imgRGBA := image.NewRGBA(image.Rectangle{image.Point{0, 0}, image.Point{scaledImgX, scaledImgY}})

	black := color.RGBA{0, 0, 0, 255}
	brown := color.RGBA{165, 42, 42, 255}
	white := color.RGBA{255, 255, 255, 255}
	cyan := color.RGBA{100, 200, 200, 0xff}

	for x := 0; x < scaledImgX; x++ {
		for y := 0; y < scaledImgY; y++ {

			switch scaledImg[x][y] {
			case 0:
				imgRGBA.Set(x, y, black)
			case 1:
				imgRGBA.Set(x, y, brown)
			case 2:
				imgRGBA.Set(x, y, white)

			case 3:
				imgRGBA.Set(x, y, cyan)
			}
		}
	}
	var img image.Image = imgRGBA
	return img
}

func getScaledMapGrid() [][]byte {
	minX := -1
	minY := -1
	maxX := -1
	maxY := -1
	for x := 0; x < len(mapInfo.mapGrid); x++ {
		for y := 0; y < len(mapInfo.mapGrid[0]); y++ {
			if mapInfo.mapGrid[x][y] != 0 {
				if minX == -1 {
					minX = x
				}
				if minY == -1 {
					minY = y
				}
				maxX = x
				maxY = y
			}
		}
	}
	scaledMapGrid := make([][]byte, (maxX - minX)) // Make the outer slice and give it size rowBits
	for i := 0; i < (maxX - minX); i++ {
		scaledMapGrid[i] = make([]byte, (maxY - minY)) // Make one inner slice per iteration and give it size 10
		for j := 0; j < (maxY - minY); j++ {
			scaledMapGrid[i][j] = mapInfo.mapGrid[i+minX][j+minY]
		}
	}
	return scaledMapGrid
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
var mapInfo = MapInfo{}
