package habridge

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"math"
	"strconv"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

//go:embed saved_map.jpg
var FallbackMapJPEG []byte

// ExportGridToValetudoMap transforms a stitched Ecovacs mapGrid into a ValetudoMap JSON v2 wire struct.
func ExportGridToValetudoMap(grid [][]byte, robotSN string, chargerX, chargerY, robotX, robotY, robotAngle int, rooms []string, roomNames map[string]string, restrictions []ecovacs.VirtualRestriction, pathPoints []ecovacs.PathPoint, cropMinX, cropMinY int) *ValetudoMap {
	width := len(grid)
	if width == 0 {
		return NewEmptyValetudoMap(robotSN)
	}
	height := len(grid[0])
	if height == 0 {
		return NewEmptyValetudoMap(robotSN)
	}

	vMap := NewEmptyValetudoMap(robotSN)
	pixelSize := 50
	vMap.PixelSize = pixelSize
	vMap.Size = MapSize{X: width * pixelSize, Y: height * pixelSize}

	var floorPixels []int
	var wallPixels []int
	roomPixelsMap := make(map[int][]int)

	fMinX, fMaxX, fMinY, fMaxY := width, 0, height, 0
	wMinX, wMaxX, wMinY, wMaxY := width, 0, height, 0

	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			val := grid[x][y]
			// Ecovacs: 1 = floor, 2 = obstacle/wall, 3 = carpet, >3 = rooms
			if val == 1 || val == 3 {
				floorPixels = append(floorPixels, x, y)
				if x < fMinX { fMinX = x }
				if x > fMaxX { fMaxX = x }
				if y < fMinY { fMinY = y }
				if y > fMaxY { fMaxY = y }
			} else if val == 2 {
				wallPixels = append(wallPixels, x, y)
				if x < wMinX { wMinX = x }
				if x > wMaxX { wMaxX = x }
				if y < wMinY { wMinY = y }
				if y > wMaxY { wMaxY = y }
			} else if val > 3 {
				floorPixels = append(floorPixels, x, y)
				if x < fMinX { fMinX = x }
				if x > fMaxX { fMaxX = x }
				if y < fMinY { fMinY = y }
				if y > fMaxY { fMaxY = y }

				roomId := int(val) - 4
				roomPixelsMap[roomId] = append(roomPixelsMap[roomId], x, y)
			}
		}
	}

	if len(floorPixels) > 0 {
		vMap.Layers = append(vMap.Layers, MapLayer{
			Type:   "floor",
			Pixels: floorPixels,
			Dimensions: LayerDimensions{
				X: DimensionRange{Min: fMinX, Max: fMaxX},
				Y: DimensionRange{Min: fMinY, Max: fMaxY},
			},
		})
	}

	if len(wallPixels) > 0 {
		vMap.Layers = append(vMap.Layers, MapLayer{
			Type:   "wall",
			Pixels: wallPixels,
			Dimensions: LayerDimensions{
				X: DimensionRange{Min: wMinX, Max: wMaxX},
				Y: DimensionRange{Min: wMinY, Max: wMaxY},
			},
		})
	}

	for roomId, pixels := range roomPixelsMap {
		rMinX, rMaxX, rMinY, rMaxY := width, 0, height, 0
		for i := 0; i < len(pixels); i += 2 {
			px, py := pixels[i], pixels[i+1]
			if px < rMinX { rMinX = px }
			if px > rMaxX { rMaxX = px }
			if py < rMinY { rMinY = py }
			if py > rMaxY { rMaxY = py }
		}
		roomLetter := string(rune('A' + roomId))
		segName := fmt.Sprintf("Room %s", roomLetter)
		if customName, ok := roomNames[strconv.Itoa(roomId)]; ok && customName != "" {
			segName = customName
		}
		vMap.Layers = append(vMap.Layers, MapLayer{
			Type:   "segment",
			Pixels: pixels,
			Dimensions: LayerDimensions{
				X: DimensionRange{Min: rMinX, Max: rMaxX},
				Y: DimensionRange{Min: rMinY, Max: rMaxY},
			},
			MetaData: LayerMetaData{
				SegmentID: strconv.Itoa(roomId),
				Name:      segName,
			},
		})
	}

	// Add Charger & Robot location entities in world coordinates (mm)
	if chargerX >= 0 && chargerY >= 0 && chargerX < width && chargerY < height {
		vMap.Entities = append(vMap.Entities, MapEntity{
			Type:   "charger_location",
			Points: []int{chargerX * pixelSize, chargerY * pixelSize},
		})
	}

	if robotX >= 0 && robotY >= 0 && robotX < width && robotY < height {
		vMap.Entities = append(vMap.Entities, MapEntity{
			Type:   "robot_position",
			Points: []int{robotX * pixelSize, robotY * pixelSize},
			MetaData: EntityMetaData{
				Angle: robotAngle,
			},
		})
	}

	// Convert virtual restrictions (robot mm centered at (400, 400)) to map world coordinates (mm)
	offsetWorldX := (400 - cropMinX) * pixelSize
	offsetWorldY := (400 - cropMinY) * pixelSize

	for _, r := range restrictions {
		if r.Type == "virtual_wall" && len(r.Points) == 4 {
			vx1 := r.Points[0] + offsetWorldX
			vy1 := r.Points[1] + offsetWorldY
			vx2 := r.Points[2] + offsetWorldX
			vy2 := r.Points[3] + offsetWorldY
			vMap.Entities = append(vMap.Entities, MapEntity{
				Type:   "virtual_wall",
				Points: []int{vx1, vy1, vx2, vy2},
			})
		} else if r.Type == "no_go_area" && len(r.Points) == 8 {
			var vPts []int
			for i := 0; i < 8; i += 2 {
				vPts = append(vPts, r.Points[i]+offsetWorldX, r.Points[i+1]+offsetWorldY)
			}
			vMap.Entities = append(vMap.Entities, MapEntity{
				Type:   "no_go_area",
				Points: vPts,
			})
		}
	}

	// Convert path trajectory points to map world coordinates (mm)
	if len(pathPoints) > 0 {
		var currentSegment []int
		for _, pt := range pathPoints {
			vx := pt.X + offsetWorldX
			vy := pt.Y + offsetWorldY
			if !pt.Connected && len(currentSegment) > 0 {
				vMap.Entities = append(vMap.Entities, MapEntity{
					Type:   "path",
					Points: currentSegment,
				})
				currentSegment = []int{}
			}
			currentSegment = append(currentSegment, vx, vy)
		}
		if len(currentSegment) > 0 {
			vMap.Entities = append(vMap.Entities, MapEntity{
				Type:   "path",
				Points: currentSegment,
			})
		}
	}

	return vMap
}

// GridToImage converts a 2D byte grid directly into an RGBA image and renders entities onto it.
func GridToImage(grid [][]byte, chargerX, chargerY, robotX, robotY, robotAngle int, restrictions []ecovacs.VirtualRestriction, pathPoints []ecovacs.PathPoint, cropMinX, cropMinY int) image.Image {
	width := len(grid)
	if width == 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	height := len(grid[0])
	if height == 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			var c color.RGBA
			switch grid[x][y] {
			case 1:
				c = color.RGBA{15, 10, 222, 255} // floor
			case 2:
				c = color.RGBA{255, 255, 255, 255} // wall
			case 3:
				c = color.RGBA{100, 200, 200, 255} // carpet
			default:
				if grid[x][y] >= 4 {
					roomColors := []color.RGBA{
						{80, 130, 240, 255}, // Room A: Soft Blue
						{90, 200, 140, 255}, // Room B: Soft Green
						{240, 180, 70, 255}, // Room C: Amber
						{170, 120, 240, 255}, // Room D: Purple
						{70, 200, 200, 255}, // Room E: Teal
					}
					rIdx := int(grid[x][y]-4) % len(roomColors)
					c = roomColors[rIdx]

					// Draw crisp room outline borders
					val := grid[x][y]
					for _, off := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
						nx, ny := x+off[0], y+off[1]
						if nx >= 0 && nx < width && ny >= 0 && ny < height {
							nVal := grid[nx][ny]
							if nVal != val && (nVal >= 4 || nVal == 1 || nVal == 0) {
								c = color.RGBA{235, 240, 255, 255}
								break
							}
						}
					}
				} else {
					c = color.RGBA{0, 0, 0, 255} // empty
				}
			}
			img.Set(x, y, c)
		}
	}

	DrawEntities(img, chargerX, chargerY, robotX, robotY, robotAngle, restrictions, pathPoints, cropMinX, cropMinY)
	return img
}

// DrawEntitiesOnImage renders entities onto any image (including fallback JPEG).
func DrawEntitiesOnImage(src image.Image, chargerX, chargerY, robotX, robotY, robotAngle int, restrictions []ecovacs.VirtualRestriction, pathPoints []ecovacs.PathPoint, cropMinX, cropMinY int) image.Image {
	bounds := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				rgba.Set(x, y, src.At(x, y))
			}
		}
	}
	DrawEntities(rgba, chargerX, chargerY, robotX, robotY, robotAngle, restrictions, pathPoints, cropMinX, cropMinY)
	return rgba
}

// DrawEntities renders the paths, virtual restrictions, charging dock, and robot vacuum onto the image.
func DrawEntities(img *image.RGBA, chargerX, chargerY, robotX, robotY, robotAngle int, restrictions []ecovacs.VirtualRestriction, pathPoints []ecovacs.PathPoint, cropMinX, cropMinY int) {
	bounds := img.Bounds()

	// 1. Draw Paths (if any)
	if len(pathPoints) > 0 {
		offsetWorldX := (400 - cropMinX) * 50
		offsetWorldY := (400 - cropMinY) * 50
		pathColor := color.RGBA{128, 216, 255, 200} // Sky blue
		var prevX, prevY int
		havePrev := false
		for _, pt := range pathPoints {
			px := (pt.X + offsetWorldX) / 50
			py := (pt.Y + offsetWorldY) / 50
			if havePrev && pt.Connected {
				drawLine(img, prevX, prevY, px, py, pathColor)
			}
			prevX, prevY = px, py
			havePrev = true
		}
	}

	// 2. Draw Virtual Restrictions (if any)
	if len(restrictions) > 0 {
		offsetWorldX := (400 - cropMinX) * 50
		offsetWorldY := (400 - cropMinY) * 50
		wallColor := color.RGBA{244, 67, 54, 255} // Bright red
		for _, r := range restrictions {
			if r.Type == "virtual_wall" && len(r.Points) == 4 {
				x1 := (r.Points[0] + offsetWorldX) / 50
				y1 := (r.Points[1] + offsetWorldY) / 50
				x2 := (r.Points[2] + offsetWorldX) / 50
				y2 := (r.Points[3] + offsetWorldY) / 50
				drawThickLine(img, x1, y1, x2, y2, 2, wallColor)
			} else if r.Type == "no_go_area" && len(r.Points) == 8 {
				for i := 0; i < 8; i += 2 {
					nextI := (i + 2) % 8
					x1 := (r.Points[i] + offsetWorldX) / 50
					y1 := (r.Points[i+1] + offsetWorldY) / 50
					x2 := (r.Points[nextI] + offsetWorldX) / 50
					y2 := (r.Points[nextI+1] + offsetWorldY) / 50
					drawThickLine(img, x1, y1, x2, y2, 2, wallColor)
				}
			}
		}
	}

	// 3. Draw Charging Dock
	if chargerX >= bounds.Min.X && chargerX < bounds.Max.X && chargerY >= bounds.Min.Y && chargerY < bounds.Max.Y {
		borderColor := color.RGBA{27, 94, 32, 255}   // Dark green border
		dockColor := color.RGBA{0, 230, 118, 255}    // Bright emerald green
		contactColor := color.RGBA{255, 214, 0, 255} // Gold charging contacts

		// 9 wide x 6 tall base
		for dx := -4; dx <= 4; dx++ {
			for dy := -3; dy <= 2; dy++ {
				x := chargerX + dx
				y := chargerY + dy
				if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					if dx == -4 || dx == 4 || dy == -3 || dy == 2 {
						img.Set(x, y, borderColor)
					} else {
						img.Set(x, y, dockColor)
					}
				}
			}
		}
		// Golden contacts
		for _, dy := range []int{-1, 0} {
			for _, dx := range []int{-2, 2} {
				x := chargerX + dx
				y := chargerY + dy
				if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					img.Set(x, y, contactColor)
				}
			}
		}
	}

	// 4. Draw Robot Vacuum
	if robotX >= bounds.Min.X && robotX < bounds.Max.X && robotY >= bounds.Min.Y && robotY < bounds.Max.Y {
		rad := float64(robotAngle) * math.Pi / 180.0
		dirX := math.Cos(rad)
		dirY := math.Sin(rad)

		// Circular body (radius 5)
		for dx := -5; dx <= 5; dx++ {
			for dy := -5; dy <= 5; dy++ {
				d2 := dx*dx + dy*dy
				if d2 > 27 {
					continue
				}
				x := robotX + dx
				y := robotY + dy
				if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
					continue
				}

				if d2 > 17 {
					// Outer bumper ring: front is cyan, rest is black
					dot := float64(dx)*dirX + float64(dy)*dirY
					if dot > 2.8 {
						img.Set(x, y, color.RGBA{0, 229, 255, 255}) // Vibrant cyan front bumper
					} else {
						img.Set(x, y, color.RGBA{33, 33, 33, 255})   // Dark bumper
					}
				} else if d2 > 4 {
					// Robot clean white body
					img.Set(x, y, color.RGBA{250, 250, 250, 255})
				} else if d2 > 1 {
					// LDS laser turret housing
					img.Set(x, y, color.RGBA{55, 71, 79, 255})
				} else {
					// LDS laser diode center
					img.Set(x, y, color.RGBA{255, 87, 34, 255})
				}
			}
		}

		// Direction pointer tips
		for step := 6.0; step <= 7.0; step += 1.0 {
			tx := int(math.Round(float64(robotX) + step*dirX))
			ty := int(math.Round(float64(robotY) + step*dirY))
			if tx >= bounds.Min.X && tx < bounds.Max.X && ty >= bounds.Min.Y && ty < bounds.Max.Y {
				img.Set(tx, ty, color.RGBA{0, 229, 255, 255})
			}
		}
	}
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, col color.RGBA) {
	bounds := img.Bounds()
	dx := absVal(x1 - x0)
	dy := -absVal(y1 - y0)
	sx, sy := 1, 1
	if x0 >= x1 {
		sx = -1
	}
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy

	for {
		if x0 >= bounds.Min.X && x0 < bounds.Max.X && y0 >= bounds.Min.Y && y0 < bounds.Max.Y {
			img.Set(x0, y0, col)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func drawThickLine(img *image.RGBA, x0, y0, x1, y1 int, thickness int, col color.RGBA) {
	for ox := -thickness / 2; ox <= thickness/2; ox++ {
		for oy := -thickness / 2; oy <= thickness/2; oy++ {
			drawLine(img, x0+ox, y0+oy, x1+ox, y1+oy, col)
		}
	}
}

func absVal(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ExportImageToValetudoMap transforms an image (e.g. from /getMapData endpoint) into a ValetudoMap v2 wire struct.
func ExportImageToValetudoMap(img image.Image, robotSN string, chargerX, chargerY, robotX, robotY, robotAngle int) *ValetudoMap {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if (width <= 1 || height <= 1) && len(FallbackMapJPEG) > 0 {
		if fallbackImg, _, err := image.Decode(bytes.NewReader(FallbackMapJPEG)); err == nil {
			img = fallbackImg
			bounds = img.Bounds()
			width = bounds.Dx()
			height = bounds.Dy()
		}
	}

	vMap := NewEmptyValetudoMap(robotSN)
	if width <= 1 && height <= 1 {
		return vMap
	}

	pixelSize := 50 // 50mm per pixel
	vMap.PixelSize = pixelSize
	vMap.Size = MapSize{X: width * pixelSize, Y: height * pixelSize}

	var floorPixels []int
	var wallPixels []int

	fMinX, fMaxX, fMinY, fMaxY := width, 0, height, 0
	wMinX, wMaxX, wMinY, wMaxY := width, 0, height, 0

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := (r*299 + g*587 + b*114) / 1000 >> 8

			relX := x - bounds.Min.X
			relY := y - bounds.Min.Y

			if lum > 180 {
				floorPixels = append(floorPixels, relX, relY)
				if relX < fMinX { fMinX = relX }
				if relX > fMaxX { fMaxX = relX }
				if relY < fMinY { fMinY = relY }
				if relY > fMaxY { fMaxY = relY }
			} else if lum > 30 && lum <= 180 {
				wallPixels = append(wallPixels, relX, relY)
				if relX < wMinX { wMinX = relX }
				if relX > wMaxX { wMaxX = relX }
				if relY < wMinY { wMinY = relY }
				if relY > wMaxY { wMaxY = relY }
			}
		}
	}

	if len(floorPixels) > 0 {
		vMap.Layers = append(vMap.Layers, MapLayer{
			Type:   "floor",
			Pixels: floorPixels,
			Dimensions: LayerDimensions{
				X: DimensionRange{Min: fMinX, Max: fMaxX},
				Y: DimensionRange{Min: fMinY, Max: fMaxY},
			},
		})
	}

	if len(wallPixels) > 0 {
		vMap.Layers = append(vMap.Layers, MapLayer{
			Type:   "wall",
			Pixels: wallPixels,
			Dimensions: LayerDimensions{
				X: DimensionRange{Min: wMinX, Max: wMaxX},
				Y: DimensionRange{Min: wMinY, Max: wMaxY},
			},
		})
	}

	// Place charger & robot (use live coordinates if available, else center)
	if len(floorPixels) > 0 {
		midX := (fMinX + fMaxX) / 2
		midY := (fMinY + fMaxY) / 2

		finalChargerX := midX
		finalChargerY := midY
		if chargerX > 0 && chargerY > 0 {
			finalChargerX = chargerX
			finalChargerY = chargerY
		}

		finalRobotX := midX
		finalRobotY := midY
		finalRobotAngle := 0
		if robotX > 0 && robotY > 0 {
			finalRobotX = robotX
			finalRobotY = robotY
			finalRobotAngle = robotAngle
		}

		if finalChargerX < 0 { finalChargerX = 0 }
		if finalChargerX >= width { finalChargerX = width - 1 }
		if finalChargerY < 0 { finalChargerY = 0 }
		if finalChargerY >= height { finalChargerY = height - 1 }

		if finalRobotX < 0 { finalRobotX = 0 }
		if finalRobotX >= width { finalRobotX = width - 1 }
		if finalRobotY < 0 { finalRobotY = 0 }
		if finalRobotY >= height { finalRobotY = height - 1 }

		vMap.Entities = append(vMap.Entities,
			MapEntity{
				Type:   "charger_location",
				Points: []int{finalChargerX * pixelSize, finalChargerY * pixelSize},
			},
			MapEntity{
				Type:   "robot_position",
				Points: []int{finalRobotX * pixelSize, finalRobotY * pixelSize},
				MetaData: EntityMetaData{
					Angle: finalRobotAngle,
				},
			},
		)
	}

	return vMap
}

// EncodePNGWithValetudoMap encodes an image as PNG and embeds the ValetudoMap JSON
// as a compressed zTXt chunk with keyword "ValetudoMap", as expected by HA vacuum map cards.
func EncodePNGWithValetudoMap(img image.Image, vMap *ValetudoMap) ([]byte, error) {
	bounds := img.Bounds()
	if (bounds.Dx() <= 1 || bounds.Dy() <= 1) && len(FallbackMapJPEG) > 0 {
		if fallbackImg, _, err := image.Decode(bytes.NewReader(FallbackMapJPEG)); err == nil {
			img = fallbackImg
			bounds = img.Bounds()
		}
	}
	if bounds.Dx() <= 1 && bounds.Dy() <= 1 {
		// Minimum 10x10 transparent canvas
		img = image.NewRGBA(image.Rect(0, 0, 10, 10))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	rawPNG := buf.Bytes()

	vMapJSON, err := json.Marshal(vMap)
	if err != nil {
		return nil, err
	}

	// Compress JSON using zlib
	var zlibBuf bytes.Buffer
	zw := zlib.NewWriter(&zlibBuf)
	if _, err := zw.Write(vMapJSON); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	compressed := zlibBuf.Bytes()

	// zTXt chunk payload: keyword (11B) + \x00 + compression method (0x00) + compressed data
	keyword := []byte("ValetudoMap\x00\x00")
	chunkData := append(keyword, compressed...)

	length := uint32(len(chunkData))
	chunkType := []byte("zTXt")

	hasher := crc32.NewIEEE()
	hasher.Write(chunkType)
	hasher.Write(chunkData)
	checksum := hasher.Sum32()

	var chunk bytes.Buffer
	binary.Write(&chunk, binary.BigEndian, length)
	chunk.Write(chunkType)
	chunk.Write(chunkData)
	binary.Write(&chunk, binary.BigEndian, checksum)

	// Insert zTXt chunk right before IEND
	iendIdx := bytes.LastIndex(rawPNG, []byte("IEND"))
	if iendIdx >= 4 {
		insertPos := iendIdx - 4
		var finalPNG bytes.Buffer
		finalPNG.Write(rawPNG[:insertPos])
		finalPNG.Write(chunk.Bytes())
		finalPNG.Write(rawPNG[insertPos:])
		return finalPNG.Bytes(), nil
	}

	return append(rawPNG, chunk.Bytes()...), nil
}

func NewEmptyValetudoMap(robotSN string) *ValetudoMap {
	return &ValetudoMap{
		Class: "ValetudoMap",
		MetaData: MapMetaData{
			Version: 2,
			Robot: RobotInfo{
				Manufacturer: "Ecovacs",
				Model:        "Deebot OZMO 930",
			},
		},
		Size:      MapSize{X: 0, Y: 0},
		PixelSize: 50, // 50mm / 5cm per pixel
		Layers:    make([]MapLayer, 0),
		Entities:  make([]MapEntity, 0),
	}
}

// BuildCameraRooms converts map segment layers and optional cropped room polygons
// into the camera attributes.rooms map consumed by lovelace-xiaomi-vacuum-map-card.
func BuildCameraRooms(vMap *ValetudoMap, roomPolygons map[string][][2]int) map[string]CameraRoom {
	rooms := make(map[string]CameraRoom)
	if vMap != nil {
		for _, layer := range vMap.Layers {
			if layer.Type != "segment" || layer.MetaData.SegmentID == "" {
				continue
			}
			id := layer.MetaData.SegmentID
			room := CameraRoom{Name: layer.MetaData.Name}

			if len(layer.Pixels) >= 2 {
				var sumX, sumY int
				count := len(layer.Pixels) / 2
				for i := 0; i+1 < len(layer.Pixels); i += 2 {
					sumX += layer.Pixels[i]
					sumY += layer.Pixels[i+1]
				}
				cx, cy := sumX/count, sumY/count
				room.X, room.Y = &cx, &cy
			}

			if poly := roomPolygons[id]; len(poly) >= 3 {
				room.Outline = poly
			} else if len(layer.Pixels) >= 2 {
				x0, y0 := layer.Dimensions.X.Min, layer.Dimensions.Y.Min
				x1, y1 := layer.Dimensions.X.Max, layer.Dimensions.Y.Max
				room.X0, room.Y0, room.X1, room.Y1 = &x0, &y0, &x1, &y1
			} else {
				continue
			}

			rooms[id] = room
		}
	}

	// Include any discovered polygons not yet rasterized onto the grid.
	for id, poly := range roomPolygons {
		if _, exists := rooms[id]; exists || len(poly) < 3 {
			continue
		}
		var sumX, sumY int
		for _, pt := range poly {
			sumX += pt[0]
			sumY += pt[1]
		}
		cx, cy := sumX/len(poly), sumY/len(poly)
		name := fmt.Sprintf("Room %s", id)
		if idNum, err := strconv.Atoi(id); err == nil && idNum >= 0 && idNum < 26 {
			name = fmt.Sprintf("Room %c", 'A'+idNum)
		}
		rooms[id] = CameraRoom{
			Name:    name,
			X:       &cx,
			Y:       &cy,
			Outline: poly,
		}
	}

	return rooms
}
