package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	glicko "github.com/cass-dlcm/salmonrunskill/internal-glicko"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func CompositeOpponentUpdate(period *glicko.RatingPeriod, players []*glicko.Player, team1Result glicko.MatchResult) {
	b := len(players) - 1
	for j := 0; j < b; j++ {
		period.AddMatch1Player(players[j], players[b], team1Result)
	}
}

func DownloadFromStatInkWrapper(db *sql.DB, client http.Client) {
	var id float64
	if err := db.QueryRow("SELECT MAX(id) FROM Salmon").Scan(&id); err != nil {
		log.Panicln(err)
	}
	for {
		new_id := DownloadFromStatInk(db, client, id)
		if new_id == nil || *new_id <= id {
			return
		}
		id = *new_id
		log.Println(id)
	}
}

func DownloadFromStatInk(db *sql.DB, client http.Client, id float64) *float64 {
	url := "https://stat.ink/api/v2/salmon"
	ctx, cancelFunc := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancelFunc()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Panicln(err)
	}
	q := req.URL.Query()
	q.Add("newer_than", fmt.Sprint(id))
	q.Add("order", "asc")
	req.URL.RawQuery = q.Encode()
	resp, err := client.Do(req)
	if err != nil {
		log.Panicln(err)
	}
	var results []map[string]interface{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&results); err != nil {
		log.Panicln(err)
	}
	var new_id float64
	for i := range results {
		var t string
		var players []string
		var hazardLevel string
		var win bool
		if v1, ok := results[i]["start_at"].(map[string]interface{}); ok {
			if v2, ok := v1["iso8601"].(string); ok {
				t = v2
			}
		}
		if v1, ok := results[i]["my_data"].(map[string]interface{}); ok {
			if v2, ok := v1["splatnet_id"].(string); ok {
				players = append(players, v2)
			}
		}
		if v1, ok := results[i]["teammates"].([]interface{}); ok {
			for j := range v1 {
				if v2, ok := v1[j].(map[string]interface{}); ok {
					if v3, ok := v2["splatnet_id"].(string); ok {
						players = append(players, v3)
					}
				}
			}
		}
		if v, ok := results[i]["danger_rate"].(string); ok {
			hazardLevel = v
		}
		if v, ok := results[i]["is_cleared"].(bool); ok {
			win = v
		}
		if v1, ok := results[i]["id"].(float64); ok {
			new_id = v1
		} else {
			log.Println(results[i]["id"])
		}
		sort.Strings(players)
		switch len(players) {
		case 1:
			if _, err := db.Exec("INSERT INTO Salmon (id, start_time, player_one, hazard_level, win) VALUES (?, ?, ?, ?, ?);", new_id, t, players[0], hazardLevel, win); err != nil {
				log.Println(err)
			}
		case 2:
			if _, err := db.Exec("INSERT INTO Salmon (id, start_time, player_one, player_two, hazard_level, win) VALUES (?, ?, ?, ?, ?, ?);", new_id, t, players[0], players[1], hazardLevel, win); err != nil {
				log.Println(err)
			}
		case 3:
			if _, err := db.Exec("INSERT INTO Salmon (id, start_time, player_one, player_two, player_three, hazard_level, win) VALUES (?, ?, ?, ?, ?, ?, ?);", new_id, t, players[0], players[1], players[2], hazardLevel, win); err != nil {
				log.Println(err)
			}
		case 4:
			if _, err := db.Exec("INSERT INTO Salmon (id, start_time, player_one, player_two, player_three, player_four, hazard_level, win) VALUES (?, ?, ?, ?, ?, ?, ?, ?);", new_id, t, players[0], players[1], players[2], players[3], hazardLevel, win); err != nil {
				log.Println(err)
			}
		}
	}
	return &new_id
}

type playersArr []player

type cpusArr []player

func (c cpusArr) Len() int {
	return len(c)
}

func (c cpusArr) Less(i, j int) bool {
	iFloat, err := strconv.ParseFloat(c[i].name, 64)
	if err != nil {
		log.Panicln(err)
	}
	jFloat, err := strconv.ParseFloat(c[j].name, 64)
	if err != nil {
		log.Panicln(err)
	}
	return iFloat < jFloat
}

func (c cpusArr) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c cpusArr) Rank(period *glicko.RatingPeriod) {
	for i := 1; i < len(c); i++ {
		for j := 0; j < i; j++ {
			period.AddMatch2Player(c[i].glickoPlayer, c[j].glickoPlayer, glicko.MATCH_RESULT_WIN)
		}
	}
}

type cpuMap map[string]*glicko.Player

func (m cpuMap) SortAndRank(period *glicko.RatingPeriod) {
	cpuArr := cpusArr{}
	for i := range m {
		cpuArr = append(cpuArr, player{i, m[i]})
	}
	sort.Sort(cpuArr)
	cpuArr.Rank(period)
}

type player struct {
	name         string
	glickoPlayer *glicko.Player
}

func (p playersArr) Len() int {
	return len(p)
}

func (p playersArr) Less(i, j int) bool {
	return p[i].glickoPlayer.Rating().R()-p[i].glickoPlayer.Rating().Rd()*3 < p[j].glickoPlayer.Rating().R()-p[j].glickoPlayer.Rating().Rd()*3
}

func (p playersArr) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

type playerMap map[string]*glicko.Player

func (m playerMap) ToSlice() playersArr {
	playerArr := playersArr{}
	for i := range m {
		playerArr = append(playerArr, player{i, m[i]})
	}
	sort.Sort(playerArr)
	return playerArr
}

func main() {
	db, err := sql.Open("sqlite", "salmon.db")
	if err != nil {
		log.Panicln(err)
	}
	players := playerMap{}
	computerOpponents := cpuMap{}

	DownloadFromStatInkWrapper(db, http.Client{})

	period := glicko.NewRatingPeriod()

	type game struct {
		id          int
		t           string
		player1     string
		player2     *string
		player3     *string
		player4     *string
		hazardLevel string
		win         bool
	}

	query, err := db.Query("SELECT DISTINCT * FROM Salmon")
	if err != nil {
		log.Panicln(err)
	}

	for query.Next() {
		var tempGame game
		if err := query.Scan(&tempGame.id, &tempGame.t, &tempGame.player1, &tempGame.player2, &tempGame.player3, &tempGame.player4, &tempGame.hazardLevel, &tempGame.win); err != nil {
			log.Panicln(err)
		}
		playerCount := 1
		if players[tempGame.player1] == nil {
			players[tempGame.player1] = glicko.NewPlayer(glicko.NewRating(0, 500, .06))
		}
		if tempGame.player2 != nil {
			if players[*tempGame.player2] == nil {
				players[*tempGame.player2] = glicko.NewPlayer(glicko.NewRating(0, 500, .06))
			}
			playerCount++
		}
		if tempGame.player3 != nil {
			if players[*tempGame.player3] == nil {
				players[*tempGame.player3] = glicko.NewPlayer(glicko.NewRating(0, 500, .06))
			}
			playerCount++
		}
		if tempGame.player4 != nil {
			if players[*tempGame.player4] == nil {
				players[*tempGame.player4] = glicko.NewPlayer(glicko.NewRating(0, 500, .06))
			}
			playerCount++
		}
		if computerOpponents[tempGame.hazardLevel] == nil {
			computerOpponents[tempGame.hazardLevel] = glicko.NewPlayer(glicko.NewRating(0, 500, .06))
		}

		result := glicko.MATCH_RESULT_LOSS
		if tempGame.win {
			result = glicko.MATCH_RESULT_WIN
		}
		switch playerCount {
		case 1:
			CompositeOpponentUpdate(period, []*glicko.Player{players[tempGame.player1], computerOpponents[tempGame.hazardLevel]}, result)
		case 2:
			CompositeOpponentUpdate(period, []*glicko.Player{players[tempGame.player1], players[*tempGame.player2], computerOpponents[tempGame.hazardLevel]}, result)
		case 3:
			CompositeOpponentUpdate(period, []*glicko.Player{players[tempGame.player1], players[*tempGame.player2], players[*tempGame.player3], computerOpponents[tempGame.hazardLevel]}, result)
		case 4:
			CompositeOpponentUpdate(period, []*glicko.Player{players[tempGame.player1], players[*tempGame.player2], players[*tempGame.player3], players[*tempGame.player4], computerOpponents[tempGame.hazardLevel]}, result)
		}

	}
	computerOpponents.SortAndRank(period)

	period.Calculate()

	sortedPlayersArr := players.ToSlice()

	playersList, err := os.OpenFile("playerRankings.txt", os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Panicln(err)
	}

	for i := range sortedPlayersArr {
		if _, err := fmt.Fprintf(playersList, "%s: %f (%f %f)\n", sortedPlayersArr[i].name, sortedPlayersArr[i].glickoPlayer.Rating().R()-sortedPlayersArr[i].glickoPlayer.Rating().Rd()*3, sortedPlayersArr[i].glickoPlayer.Rating().R(), sortedPlayersArr[i].glickoPlayer.Rating().Rd()); err != nil {
			log.Panicln(err)
		}
	}
}
