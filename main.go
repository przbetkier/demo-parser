package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/golang/geo/r2"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	dem "github.com/markus-wa/demoinfocs-golang"
	"github.com/markus-wa/demoinfocs-golang/events"
	"github.com/markus-wa/demoinfocs-golang/metadata"
)

type Request struct {
	DemoUrl string `json:"demoUrl"`
	MatchId string `json:"matchId"`
}

type Response struct {
	Message string `json:"message"`
}

type RequestBody struct {
	MatchId string       `json:"matchId"`
	Data    []PlayerData `json:"data"`
}

type PlayerData struct {
	Nickname       string        `json:"nickname"`
	BombPlants     int           `json:"plants"`
	Defusals       int           `json:"defusals"`
	PlayersFlashed int           `json:"flashed"`
	Kills          []PlayerKill  `json:"kills"`
	Deaths         []PlayerDeath `json:"deaths"`
}

type PlayerDeath struct {
	Killer         string   `json:"killer"`
	KillerPosition r2.Point `json:"kPos"`
	VictimPosition r2.Point `json:"vPos"`
	WasWallbang    bool     `json:"wb"`
	WasHeadshot    bool     `json:"hs"`
	WasEntry       bool     `json:"entry"`
	Weapon         string   `json:"weapon"`
}

type PlayerKill struct {
	Victim         string   `json:"victim"`
	KillerPosition r2.Point `json:"kPos"`
	VictimPosition r2.Point `json:"vPos"`
	WasWallbang    bool     `json:"wb"`
	WasHeadshot    bool     `json:"hs"`
	WasEntry       bool     `json:"entry"`
	Weapon         string   `json:"weapon"`
}

var exists = struct{}{}

type set struct {
	m map[string]struct{}
}

func NewSet() *set {
	s := &set{}
	s.m = make(map[string]struct{})
	return s
}

func (s *set) Add(value string) {
	s.m[value] = exists
}

func (s *set) Remove(value string) {
	delete(s.m, value)
}

func (s *set) Contains(value string) bool {
	_, c := s.m[value]
	return c
}

func main() {
	//Comment lambda execution for local testing
	lambda.Start(Handler)

	// Uncomment that part for local testing
	//_ = os.Setenv("API_ENDPOINT", "http://localhost:8080/tuscan-api/demo-stats")
	//req := Request{
	//	DemoUrl: "https://demos-europe-west2.faceit-cdn.net/csgo/b21ef50d-247f-4ca4-a1b2-01d6ab2d3d9d.dem.gz",
	//	MatchId: "1-5681cb94-b900-48ff-a66b-13eca6819268",
	//}
	//Run(req)
}

// Comment Handler function for local testing
func Handler(request Request) (Response, error) {
	Run(request)
	return Response{
		Message: fmt.Sprintf("Successfuly processed %s", request.MatchId)}, nil
}

func Run(req Request) {

	log.Printf("Received request to parse demo data for matchId: [%s].\n", req.MatchId)

	var playerDatas []PlayerData
	nicknames := NewSet()

	startDate := time.Now()

	fileUrl := req.DemoUrl
	zippedPath := fmt.Sprintf("/tmp/%s-demo.dem.gz", req.MatchId)

	if err := DownloadFile(zippedPath, fileUrl); err != nil {
		panic(err)
	}

	Unzip(zippedPath)
	if err := os.Remove(zippedPath); err != nil {
		panic(err)
	} else {
		log.Printf("Successfully removed zipped file [%s]!", zippedPath)
	}

	DemoFileName := strings.TrimSuffix(zippedPath, ".gz")

	f, err := os.Open(DemoFileName)
	checkError(err)

	defer f.Close()

	p := dem.NewParser(f)

	var header, headerErr = p.ParseHeader()
	checkError(headerErr)

	mapMetadata := metadata.MapNameToMap[header.MapName]

	log.Printf("MAP: %s\n", header.MapName)

	// initial game/round values:
	var matchStart = false
	var firstRoundSkipped = false
	var firstKillDone = false

	p.RegisterEventHandler(func(e events.PlayerConnect) {
		if !matchStart {
			nicknames.Add(e.Player.Name)
			log.Printf("[%s] connected. Adding to set.", e.Player.Name)
		}
	})

	// Initialize empty data-object when match start
	p.RegisterEventHandler(func(e events.MatchStart) {
		matchStart = true
		if firstRoundSkipped {
			for nickname := range nicknames.m {
				playerDatas = append(playerDatas,
					PlayerData{Nickname: nickname,
						BombPlants:     0,
						Defusals:       0,
						PlayersFlashed: 0,
						Kills:          []PlayerKill{},
						Deaths:         []PlayerDeath{},
					})
			}
		}
	})

	// Reset firstKillDone flag to track entry kills
	p.RegisterEventHandler(func(e events.RoundStart) {
		firstKillDone = false
	})

	p.RegisterEventHandler(func(e events.RoundEnd) {
		if matchStart {
			firstRoundSkipped = true
		}
	})

	p.RegisterEventHandler(func(e events.BombPlanted) {
		for i, v := range playerDatas {
			if v.Nickname == e.Player.Name {
				playerDatas[i].BombPlants++
			}
		}
	})

	p.RegisterEventHandler(func(e events.BombDefused) {
		for i, v := range playerDatas {
			if v.Nickname == e.Player.Name {
				playerDatas[i].Defusals++
			}
		}
	})

	p.RegisterEventHandler(func(e events.PlayerFlashed) {
		for i, v := range playerDatas {
			if v.Nickname == e.Attacker.Name && e.Player.Team != e.Attacker.Team && e.Player.Name != "GOTV" && e.Player.FlashDuration > 2.0 {
				playerDatas[i].PlayersFlashed++
			}
		}
	})

	p.RegisterEventHandler(func(e events.Kill) {
		if matchStart && firstRoundSkipped {
			var killer string
			if e.Killer != nil {
				killer = e.Killer.Name
			} else {
				killer = e.Victim.Name
			}
			victim := e.Victim.Name
			var xKiller, yKiller float64

			if killer != victim {
				xKiller, yKiller = mapMetadata.TranslateScale(e.Killer.Position.X, e.Killer.Position.Y)
			} else {
				xKiller, yKiller = mapMetadata.TranslateScale(e.Victim.Position.X, e.Victim.Position.Y)
			}
			xVictim, yVictim := mapMetadata.TranslateScale(e.Victim.Position.X, e.Victim.Position.Y)
			killerPos := r2.Point{X: xKiller, Y: yKiller}
			victimPos := r2.Point{X: xVictim, Y: yVictim}

			for i, v := range playerDatas {
				if v.Nickname == killer && killer != victim {

					playerDatas[i].Kills = append(playerDatas[i].Kills,
						PlayerKill{
							Victim:         e.Victim.String(),
							KillerPosition: killerPos,
							VictimPosition: victimPos,
							WasWallbang:    e.PenetratedObjects > 0,
							WasHeadshot:    e.IsHeadshot,
							WasEntry:       !firstKillDone,
							Weapon:         e.Weapon.String(),
						})

					log.Printf("%s killed %s with %s\n", killer, victim, e.Weapon.String())
				}
			}

			for i, v := range playerDatas {
				if v.Nickname == victim {
					playerDatas[i].Deaths = append(playerDatas[i].Deaths,
						PlayerDeath{
							Killer:         killer,
							KillerPosition: killerPos,
							VictimPosition: victimPos,
							WasWallbang:    e.PenetratedObjects > 0,
							WasHeadshot:    e.IsHeadshot,
							WasEntry:       !firstKillDone,
							Weapon:         e.Weapon.String(),
						})
				}
			}

			if !firstKillDone {
				firstKillDone = true
			}
		}
	})

	// Parse to end
	err = p.ParseToEnd()
	if err != nil {
		panic(err)
	}

	os.Remove(DemoFileName)
	log.Printf("Successfuly removed [%s]!", DemoFileName)

	payload := RequestBody{MatchId: req.MatchId, Data: playerDatas}
	request, err := json.Marshal(payload)

	resp, err := http.Post(os.Getenv("API_ENDPOINT"), "application/json", bytes.NewBuffer(request))
	if err != nil {
		log.Fatalln(err)
	}
	if resp.StatusCode == 201 {
		log.Println("Submitted the result of parsed demo for tuscan.")
		log.Printf("Operation took: %d ms\n", time.Now().Sub(startDate).Milliseconds())
	}
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}

func DownloadFile(filepath string, url string) error {

	log.Printf("Started demo download from URL [%s] to file: [%s].", url, filepath)
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

	log.Printf("File: [%s] created!", filepath)
	return err
}

func Unzip(filename string) {

	log.Printf("Unzipping file: [%s] ", filename)
	if filename == "" {
		log.Println("Usage : gunzip sourcefile.gz")
	}

	gzipfile, err := os.Open(filename)

	if err != nil {
		log.Println(err)
	}

	reader, err := gzip.NewReader(gzipfile)
	if err != nil {
		log.Println(err)
	}
	defer reader.Close()

	NewFileName := strings.TrimSuffix(filename, ".gz")

	writer, err := os.Create(NewFileName)

	if err != nil {
		log.Println(err)
	} else {
		log.Printf("Successfully unzipped demo file to [%s].", NewFileName)
	}

	defer writer.Close()

	if _, err = io.Copy(writer, reader); err != nil {
		log.Println(err)
	}
}
