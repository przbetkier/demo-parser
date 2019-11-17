package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"github.com/markus-wa/demoinfocs-golang/metadata"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dustin/go-heatmap"
	"github.com/dustin/go-heatmap/schemes"
	"github.com/golang/geo/r2"

	dem "github.com/markus-wa/demoinfocs-golang"
	"github.com/markus-wa/demoinfocs-golang/events"

	"github.com/minio/minio-go"
)

const (
	dotSize     = 30
	opacity     = 128
	jpegQuality = 90
)

type DemoParseRequest struct {
	DemoUrl string
	Nickname string
	MatchId string
}

func main() {
	handler := http.NewServeMux()

	handler.HandleFunc("/parse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		decoder := json.NewDecoder(r.Body)
		var reqBody DemoParseRequest

		err := decoder.Decode(&reqBody)
		if err != nil {
			panic(err)
		}

		log.Println(reqBody.DemoUrl)
		log.Println(reqBody.Nickname)

		Run(reqBody)

		io.WriteString(w, "Hello world\n")
	})

	if err := http.ListenAndServe(":9090", handler); err != nil {
		panic(err)
	}
}

func Run(req DemoParseRequest) {
	startDate := time.Now()

	fileUrl := req.DemoUrl

	zippedPath := fmt.Sprintf("%s-demo.dem.gz", req.MatchId)

	if err := DownloadFile(zippedPath, fileUrl); err != nil {
		panic(err)
	}

	Unzip(zippedPath)
	DemoFileName := strings.TrimSuffix(zippedPath, ".gz")
	os.Remove(zippedPath)

	matchId := req.MatchId

	f, err := os.Open(DemoFileName)
	checkError(err)

	defer f.Close()

	p := dem.NewParser(f)
	nickname := req.Nickname

	var header, headerErr = p.ParseHeader()
	checkError(headerErr)

	fmt.Printf("MAP: %s\n", header.MapName)
	var kills = 0
	var assists = 0
	var deaths = 0
	var headshots = 0
	var matchStart = false
	var bombsPlanted = 0
	var defusals = 0
	var firstRoundSkipped = false

	p.RegisterEventHandler(func(e events.MatchStart) {
		matchStart = true
	})

	p.RegisterEventHandler(func(e events.BombPlanted) {
		if e.Player.Name == nickname {
			bombsPlanted++
		}
	})

	p.RegisterEventHandler(func(e events.BombDefused) {
		if e.Player.Name == nickname {
			defusals++
		}
	})

	p.RegisterEventHandler(func(e events.RoundEnd) {
		if matchStart {
			firstRoundSkipped = true
		}
	})

	var weapons []string

	// Register handler on kill events
	p.RegisterEventHandler(func(e events.Kill) {
		if matchStart && firstRoundSkipped {
			if e.Killer.Name == nickname {
				kills++
				weapons = append(weapons, e.Weapon.Weapon.String())
				var hs string
				if e.IsHeadshot {
					hs = " (HS)"
					headshots++
				}
				var wallBang string
				if e.PenetratedObjects > 0 {
					wallBang = " (Wallbang)"
				}
				fmt.Printf("%s <%v%s%s> %s\n", e.Killer, e.Weapon, hs, wallBang, e.Victim)
				fmt.Printf("%d kills | ", e.Killer.AdditionalPlayerInformation.Kills+1)
				fmt.Printf("%d assists | ", e.Killer.AdditionalPlayerInformation.Assists)
				fmt.Printf("%d deaths | \n", e.Killer.AdditionalPlayerInformation.Deaths)
			}
			if e.Victim.Name == nickname {
				deaths++
				fmt.Printf("%d kills | ", e.Victim.AdditionalPlayerInformation.Kills)
				fmt.Printf("%d assists | ", e.Victim.AdditionalPlayerInformation.Assists)
				fmt.Printf("%d deaths | \n", e.Victim.AdditionalPlayerInformation.Deaths+1)
			}
			if e.Assister != nil && e.Assister.Name == nickname {
				assists++
			}
		}
	})

	// Parse to end
	err = p.ParseToEnd()
	fmt.Printf("weapons here: %s", weapons)
	printUniqueValue(weapons)
	fmt.Printf("%d defusals | %d bomb plants\n", defusals, bombsPlanted)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Operation took: %d ms", time.Now().Sub(startDate).Milliseconds())

	DrawHeatMaps(nickname, matchId, DemoFileName)

	Uploader(nickname, matchId)

	os.Remove(DemoFileName)
}

func DrawHeatMaps(nickname string, matchId string, DemoFileName string) {
	f, osError := os.Open(DemoFileName)
	checkError(osError)
	defer f.Close()

	p := dem.NewParser(f)

	// Parse header (contains map-name etc.)
	header, headerErr := p.ParseHeader()
	checkError(headerErr)

	mapMetadata := metadata.MapNameToMap[header.MapName]

	// Register handler for player Kills and Deaths, triggered every kill is registered
	var deathPoints []r2.Point
	var killPoints []r2.Point

	p.RegisterEventHandler(func(e events.Kill) {
		if e.Killer.Name == nickname {
			x, y := mapMetadata.TranslateScale(e.Killer.Position.X, e.Killer.Position.Y)
			killPoints = append(killPoints, r2.Point{X: x, Y: y})
		}

		if e.Victim.Name == nickname {
			x, y := mapMetadata.TranslateScale(e.Victim.Position.X, e.Victim.Position.Y)
			deathPoints = append(deathPoints, r2.Point{X: x, Y: y})
		}
	})

	// Parse the whole demo
	var err = p.ParseToEnd()
	checkError(err)

	mapFromPoints(deathPoints, nickname, "deaths", header.MapName, matchId)
	mapFromPoints(killPoints, nickname, "kills", header.MapName, matchId)
}

func mapFromPoints(points []r2.Point, nickname string, kind string, mapName string, matchId string) {

	r2Bounds := r2.RectFromPoints(points...)
	bounds := image.Rectangle{
		Min: image.Point{X: int(r2Bounds.X.Lo), Y: int(r2Bounds.Y.Lo)},
		Max: image.Point{X: int(r2Bounds.X.Hi), Y: int(r2Bounds.Y.Hi)},
	}

	// Transform r2.Points into heatmap.DataPoints
	var data []heatmap.DataPoint
	for _, p := range points[1:] {
		// Invert Y since go-heatmap expects data to be ordered from bottom to top
		data = append(data, heatmap.P(p.X, p.Y*-1))
	}

	// Load map overview image
	fMap, err := os.Open(fmt.Sprintf("/Users/przemyslaw.betkier/Downloads/maps/%s.jpg", mapName))
	checkError(err)
	imgMap, _, err := image.Decode(fMap)
	checkError(err)

	// Create output canvas and use map overview image as base
	img := image.NewRGBA(imgMap.Bounds())
	draw.Draw(img, imgMap.Bounds(), imgMap, image.Point{}, draw.Over)

	// Generate and draw heatmap overlay on top of the overview
	imgHeatmap := heatmap.Heatmap(image.Rect(0, 0, bounds.Dx(), bounds.Dy()), data, dotSize, opacity, schemes.AlphaFire)
	draw.Draw(img, bounds, imgHeatmap, image.Point{}, draw.Over)

	// Write to stdout
	outfile, err := os.Create(fmt.Sprintf("./%s-%s-%s.jpg", matchId, nickname, kind))
	writer := bufio.NewWriter(outfile)
	err = jpeg.Encode(writer, img, &jpeg.Options{Quality: jpegQuality})
	_ = writer.Flush()

	checkError(err)
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}

func printUniqueValue(arr []string) {
	//Create a   dictionary of values for each element
	dict := make(map[string]int)
	for _, num := range arr {
		dict[num] = dict[num] + 1
	}
	fmt.Println(dict)
}

func Uploader(nickname string, matchId string) {

	accessKey := "ZTQNROOL3FFYO7U2EJJ3"
	secKey := "U0vfMldG4FKZrQ/818rMSexsJTbHT01R/Xn1TKdX/hc"
	bucketName := "tuscan-pro"

	// Initiate a client using DigitalOcean Spaces.
	client, err := minio.New("fra1.digitaloceanspaces.com", accessKey, secKey, true)
	if err != nil {
		log.Fatal(err)
	}

	// Set to public read
	userMetaData := map[string]string{"x-amz-acl": "public-read"}

	_, _ = client.FPutObject(bucketName,
		fmt.Sprintf("%s-%s-kills.jpg", matchId, nickname),
		fmt.Sprintf("./%s-%s-kills.jpg", matchId, nickname),
		minio.PutObjectOptions{UserMetadata: userMetaData})

	_, _ = client.FPutObject(bucketName,
		fmt.Sprintf("%s-%s-deaths.jpg", matchId, nickname),
		fmt.Sprintf("./%s-%s-deaths.jpg", matchId, nickname),
		minio.PutObjectOptions{UserMetadata: userMetaData})
}

func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func Unzip(filename string) {

	if filename == "" {
		fmt.Println("Usage : gunzip sourcefile.gz")
	}

	gzipfile, err := os.Open(filename)

	if err != nil {
		fmt.Println(err)
	}

	reader, err := gzip.NewReader(gzipfile)
	if err != nil {
		fmt.Println(err)
	}
	defer reader.Close()

	NewFileName := strings.TrimSuffix(filename, ".gz")

	writer, err := os.Create(NewFileName)

	if err != nil {
		fmt.Println(err)
	}

	defer writer.Close()

	if _, err = io.Copy(writer, reader); err != nil {
		fmt.Println(err)
	}
}
