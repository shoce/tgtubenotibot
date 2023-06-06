package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

const (
	NL = "\n"

	//YtEventType string = "completed"
	YtEventType string = "upcoming"
)

var (
	YamlConfigPath = "tgtubenotibot.yaml"

	KvToken       string
	KvAccountId   string
	KvNamespaceId string

	HttpClient = &http.Client{}

	TgToken      string
	TgChatId     string
	TgBossChatId string

	// https://console.cloud.google.com/apis/credentials
	YtApiKey    string
	YtChannelId string

	YtPublishedAfter string
)

func log(msg interface{}, args ...interface{}) {
	t := time.Now().Local()
	ts := fmt.Sprintf("%03d/%02d%02d/%02d%02d", t.Year()%1000, t.Month(), t.Day(), t.Hour(), t.Minute())
	fmt.Fprintf(os.Stderr, fmt.Sprintf("%s %s", ts, msg)+NL, args...)
}

func init() {
}

func monthnameru(m time.Month) string {
	switch m {
	case time.January:
		return "январь"
	case time.February:
		return "февраль"
	case time.March:
		return "март"
	case time.April:
		return "апрель"
	case time.May:
		return "май"
	case time.June:
		return "июнь"
	case time.July:
		return "июль"
	case time.August:
		return "август"
	case time.September:
		return "сентябрь"
	case time.October:
		return "октябрь"
	case time.November:
		return "ноябрь"
	case time.December:
		return "декабрь"
	}
	return "неизвестный месяц"
}

func httpPostJson(url string, data *bytes.Buffer, target interface{}) error {
	resp, err := HttpClient.Post(
		url,
		"application/json",
		data,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody := bytes.NewBuffer(nil)
	_, err = io.Copy(respBody, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}

	err = json.NewDecoder(respBody).Decode(target)
	if err != nil {
		return fmt.Errorf("json.Decode: %v", err)
	}

	return nil
}

type TgPhotoSize struct {
	FileId       string `json:"file_id"`
	FileUniqueId string `json:"file_unique_id"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type TgMessage struct {
	Id        string
	MessageId int64         `json:"message_id"`
	Photo     []TgPhotoSize `json:"photo"`
}

type TgResponse struct {
	Ok          bool       `json:"ok"`
	Description string     `json:"description"`
	Result      *TgMessage `json:"result"`
}

func tgSendPhoto(chatid, url, caption, parsemode string) (msg *TgMessage, err error) {
	if parsemode == "" {
		parsemode = "MarkdownV2"
	}
	sendphoto := map[string]interface{}{
		"chat_id":    chatid,
		"photo":      url,
		"caption":    caption,
		"parse_mode": parsemode,
	}
	sendphotojson, err := json.Marshal(sendphoto)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = httpPostJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TgToken),
		bytes.NewBuffer(sendphotojson),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("tgSendPhoto: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func main() {
	var err error

	ytsvc, err := youtube.NewService(context.TODO(), option.WithAPIKey(YtApiKey))
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}

	// https://developers.google.com/youtube/v3/docs/search/list

	s := ytsvc.Search.List([]string{"id", "snippet"}).MaxResults(1).Order("date").Type("video")
	s = s.ChannelId(YtChannelId).EventType(YtEventType)
	s = s.PublishedAfter(YtPublishedAfter)
	rs, err := s.Do()
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}

	//log("search.list:")

	//log("response: %+v", r)
	//log("items:%d", len(r.Items))
	/*
		for i, item := range rs.Items {
			log("n:%03d id:%s title:`%s`", i+1, item.Id.VideoId, item.Snippet.Title)
		}
	*/
	if len(rs.Items) == 0 {
		log("no %s events", YtEventType)
		return
	}

	//log("videos.list:")

	vid := rs.Items[0].Id.VideoId

	// https://developers.google.com/youtube/v3/docs/videos/list

	v := ytsvc.Videos.List([]string{"snippet", "liveStreamingDetails"})
	v = v.Id(vid)
	rv, err := v.Do()
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}
	//log("response: %+v", rv)
	tzmoscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}
	if len(rv.Items) == 0 {
		log("video %s not found", vid)
		return
	}
	item := rv.Items[0]

	patime, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}
	patime = patime.Add(1 * time.Second)

	sstime, err := time.Parse(time.RFC3339, item.LiveStreamingDetails.ScheduledStartTime)
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}

	thumbnailurl := ""
	if item.Snippet.Thumbnails != nil {
		switch {
		case item.Snippet.Thumbnails.Maxres != nil && item.Snippet.Thumbnails.Maxres.Url != "":
			thumbnailurl = item.Snippet.Thumbnails.Maxres.Url
		case item.Snippet.Thumbnails.Standard != nil && item.Snippet.Thumbnails.Standard.Url != "":
			thumbnailurl = item.Snippet.Thumbnails.Standard.Url
		case item.Snippet.Thumbnails.High != nil && item.Snippet.Thumbnails.High.Url != "":
			thumbnailurl = item.Snippet.Thumbnails.High.Url
		case item.Snippet.Thumbnails.Medium != nil && item.Snippet.Thumbnails.Medium.Url != "":
			thumbnailurl = item.Snippet.Thumbnails.Medium.Url
		case item.Snippet.Thumbnails.Default != nil && item.Snippet.Thumbnails.Default.Url != "":
			thumbnailurl = item.Snippet.Thumbnails.Default.Url
		}
	}

	log("published at: %s"+NL, item.Snippet.PublishedAt)

	log("thumbnail: %s"+NL, thumbnailurl)

	caption := fmt.Sprintf(
		"*«%s»*"+NL+NL+
			"%s/"+
			"%d"+NL+
			"*%s*"+NL+"(московское время)"+NL+NL+
			"https://youtu.be/%s"+NL,
		item.Snippet.Title,
		strings.ToTitle(monthnameru(sstime.In(tzmoscow).Month())),
		sstime.In(tzmoscow).Day(),
		sstime.In(tzmoscow).Format("15:04"),
		item.Id,
	)
	log("%s"+NL, caption)

	caption = strings.NewReplacer(
		"(", "\\(",
		")", "\\)",
		"[", "\\[",
		"]", "\\]",
		"{", "\\{",
		"}", "\\}",
		"~", "\\~",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"!", "\\!",
		".", "\\.",
	).Replace(caption)
	msg, err := tgSendPhoto(TgChatId, thumbnailurl, caption, "")
	if err != nil {
		log("%s", err)
		os.Exit(1)
	}
	log("posted tg message id:%s"+NL, msg.Id)

	log("published after: %s"+NL, patime.Format(time.RFC3339))
}
