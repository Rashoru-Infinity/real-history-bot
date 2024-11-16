package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var (
	configMap map[string]string
)

func GetEvent(ev *socketmode.Event) (slackevents.EventsAPIEvent, bool) {
	event, ok := ev.Data.(slackevents.EventsAPIEvent)
	return event, ok
}

func GetMessageEvent(eventsAPIEvent slackevents.EventsAPIEvent) (*slackevents.MessageEvent, bool) {
	ev, ok := eventsAPIEvent.InnerEvent.Data.(*slackevents.MessageEvent)
	return ev, ok
}

func HandleMessageEvent(ev *socketmode.Event, sockClient *socketmode.Client) {
	var (
		payload   map[string]interface{}
		author    string
		channel   string
		timestamp string
		content   string
		urls      = make([]byte, 0, 100)
	)
	eventsAPIEvent, ok := GetEvent(ev)
	if !ok {
		log.Println("GetEvent")
		return
	}
	sockClient.Ack(*ev.Request)
	msgEV, ok := GetMessageEvent(eventsAPIEvent)
	if !ok {
		log.Println("GetMessageEvent")
		return
	}
	channel = msgEV.Channel
	if err := json.Unmarshal(ev.Request.Payload, &payload); err != nil {
		log.Println("json.Unmarshal")
		return
	}
	author, ok = payload["event"].(map[string]interface{})["user"].(string)
	if !ok {
		log.Println("parse: event.user")
		return
	}
	timestamp, ok = payload["event"].(map[string]interface{})["ts"].(string)
	if !ok {
		log.Println("parse: event.ts")
		return
	}
	content, ok = payload["event"].(map[string]interface{})["text"].(string)
	if !ok {
		log.Println("parse: event.text")
		return
	}
	content += "\n"
	files, ok := payload["event"].(map[string]interface{})["files"].([]interface{})
	if !ok {
		log.Println("no file")
		goto pushrepo
	}
	for _, f := range files {
		url, ok := f.(map[string]interface{})["url_private"].(string)
		if !ok {
			log.Println("parse: event.files.url_private")
			continue
		}
		urls = append(urls, url...)
		urls = append(urls, '\n')
	}
	if len(urls) > 0 {
		content += string(urls)
	}
pushrepo:
	repoURL := configMap["GIT_REPOSITORY_URL"]
	fs := memfs.New()
	storer := memory.NewStorage()
	repo, err := git.Clone(storer, fs, &git.CloneOptions{
		URL: repoURL,
		Auth: &http.BasicAuth{
			Username: configMap["GIT_REPOSITORY_USERNAME"],
			Password: configMap["GIT_REPOSITORY_PASSWORD"],
		},
		Depth: 1,
	})
	if err != nil && !strings.Contains(err.Error(), "empty") {
		log.Println(err)
		return
	}
	delimiterPos := strings.LastIndex(repoURL, "/")
	repoName := repoURL[delimiterPos+1:]
	delimiterPos = strings.Index(repoName, ".")
	repoName = repoName[:delimiterPos]

	wt, err := repo.Worktree()
	if err != nil {
		log.Println(err)
		return
	}
	commitTime := time.Now()
	commitYear := strconv.Itoa(commitTime.UTC().Year())
	commitMonth := strconv.Itoa(int(commitTime.UTC().Month()))
	commitDay := strconv.Itoa(commitTime.UTC().Day())
	dir := "/" + repoName + "/" + commitYear + "/" + commitMonth + "/" + commitDay
	if fs.MkdirAll(dir, os.ModeAppend) != nil {
		log.Println("fs.MkdirAll")
		return
	}
	file, err := fs.OpenFile(dir+"/"+channel+".md", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		log.Println("fs.OpenFile")
		return
	}
	contentMDFmt := fmt.Sprintf("(%s) %s\n```\n%s```\n", timestamp, author, content)
	_, err = file.Write([]byte(contentMDFmt))
	if err != nil {
		log.Println("file.Write")
		return
	}
	if file.Close() != nil {
		log.Println("file.Close")
		return
	}
	_, err = wt.Add(repoName)
	if err != nil {
		log.Println("wt.Add")
		return
	}
	commitFmt := fmt.Sprintf("(%s) %s@%s", timestamp, author, content)
	_, err = wt.Commit(commitFmt, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "real-history-bot",
			Email: "realhistorybot@example.com",
			When:  commitTime,
		},
	})
	if err != nil {
		log.Println("wt.Commit")
		return
	}
	err = repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth: &http.BasicAuth{
			Username: configMap["GIT_REPOSITORY_USERNAME"],
			Password: configMap["GIT_REPOSITORY_PASSWORD"],
		},
	})
	if err != nil && len(err.Error()) > 0 {
		log.Println(err)
		return
	}
}

func HandleConnectingEvent(evt *socketmode.Event, client *socketmode.Client) {
	log.Println("connecting to slack...")
}

func HandleConnectionErrorEvent(ev *socketmode.Event, client *socketmode.Client) {
	log.Println(ev.Data)
	log.Println("connection failed. retrying later...")
}

func HandleConnectedEvent(evt *socketmode.Event, client *socketmode.Client) {
	log.Println("connected to slack")
}

func main() {
	configVariables := []string{
		"SLACK_APP_TOKEN",
		"SLACK_BOT_TOKEN",
		"GIT_REPOSITORY_URL",
		"GIT_REPOSITORY_USERNAME",
		"GIT_REPOSITORY_PASSWORD",
	}
	configMap = make(map[string]string)
	for _, key := range configVariables {
		val := os.Getenv(key)
		if len(val) == 0 {
			log.Printf("%s is empty\n", key)
			return
		}
		configMap[key] = val
	}
	appLevelToken := slack.OptionAppLevelToken(configMap["SLACK_APP_TOKEN"])
	if appLevelToken == nil {
		log.Println("slack.OptionAppLevelToken")
		return
	}
	slackClient := slack.New(configMap["SLACK_APP_TOKEN"], appLevelToken)
	if slackClient == nil {
		log.Println("slack.New")
		return
	}
	sockClient := socketmode.New(slackClient)
	if sockClient == nil {
		log.Println("socketmode.New")
		return
	}
	sockHandler := socketmode.NewSocketmodeHandler(sockClient)
	if sockHandler == nil {
		log.Println("socketmode.NewSocketmodeHandler")
		return
	}
	sockHandler.HandleEvents(slackevents.Message, HandleMessageEvent)
	sockHandler.Handle(socketmode.EventTypeConnecting, HandleConnectingEvent)
	sockHandler.Handle(socketmode.EventTypeConnectionError, HandleConnectionErrorEvent)
	sockHandler.Handle(socketmode.EventTypeConnected, HandleConnectedEvent)
	sockHandler.RunEventLoop()
}
