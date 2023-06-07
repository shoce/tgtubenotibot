package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"gopkg.in/yaml.v3"
)

const (
	NL = "\n"

	//YtEventType string = "completed"
	YtEventType string = "upcoming"
)

var (
	DEBUG bool

	YamlConfigPath string = "tgtubenotibot.yaml"

	KvToken       string
	KvAccountId   string
	KvNamespaceId string

	TgToken      string
	TgChatId     string
	TgBossChatId string

	// https://console.cloud.google.com/apis/credentials
	YtKey       string
	YtUsername  string
	YtChannelId string

	YtPublishedAfter string

	HttpClient = &http.Client{}
)

func log(msg interface{}, args ...interface{}) {
	t := time.Now().Local()
	ts := fmt.Sprintf("%03d/%02d%02d/%02d%02d", t.Year()%1000, t.Month(), t.Day(), t.Hour(), t.Minute())
	fmt.Fprintf(os.Stderr, fmt.Sprintf("%s %s", ts, msg)+NL, args...)
}

func tglog(msg interface{}, args ...interface{}) error {
	tgmsg := fmt.Sprintf(fmt.Sprintf("%s", msg)+NL, args...)

	type TgSendMessageRequest struct {
		ChatId              string `json:"chat_id"`
		Text                string `json:"text"`
		ParseMode           string `json:"parse_mode,omitempty"`
		DisableNotification bool   `json:"disable_notification"`
	}

	type TgSendMessageResponse struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageId int64 `json:"message_id"`
		} `json:"result"`
	}

	smreq := TgSendMessageRequest{
		ChatId:              TgBossChatId,
		Text:                tgmsg,
		ParseMode:           "",
		DisableNotification: true,
	}
	smreqjs, err := json.Marshal(smreq)
	if err != nil {
		return fmt.Errorf("tglog json marshal: %w", err)
	}
	smreqjsBuffer := bytes.NewBuffer(smreqjs)

	var resp *http.Response
	tgapiurl := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TgToken)
	resp, err = http.Post(
		tgapiurl,
		"application/json",
		smreqjsBuffer,
	)
	if err != nil {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s`: %w", tgapiurl, smreqjs, err)
	}

	var smresp TgSendMessageResponse
	err = json.NewDecoder(resp.Body).Decode(&smresp)
	if err != nil {
		return fmt.Errorf("tglog decode response: %w", err)
	}
	if !smresp.OK {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s` api response not ok: %+v", tgapiurl, smreqjs, smresp)
	}

	return nil
}

func GetVar(name string) (value string, err error) {
	if DEBUG {
		log("DEBUG GetVar: %s", name)
	}

	value = os.Getenv(name)
	if value != "" {
		return value, nil
	}

	if YamlConfigPath != "" {
		value, err = YamlGet(name)
		if err != nil {
			log("ERROR GetVar YamlGet %s: %v", name, err)
			return "", err
		}
		if value != "" {
			return value, nil
		}
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		if v, err := KvGet(name); err != nil {
			log("ERROR GetVar KvGet %s: %v", name, err)
			return "", err
		} else {
			value = v
		}
	}

	return value, nil
}

func SetVar(name, value string) (err error) {
	if DEBUG {
		log("DEBUG SetVar: %s: %s", name, value)
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		err = KvSet(name, value)
		if err != nil {
			return err
		}
		return nil
	}

	if YamlConfigPath != "" {
		err = YamlSet(name, value)
		if err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("not kv credentials nor yaml config path provided to save to")
}

func YamlGet(name string) (value string, err error) {
	configf, err := os.Open(YamlConfigPath)
	if err != nil {
		if DEBUG {
			log("WARNING os.Open config file %s: %v", YamlConfigPath, err)
		}
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer configf.Close()

	configm := make(map[interface{}]interface{})
	if err = yaml.NewDecoder(configf).Decode(&configm); err != nil {
		if DEBUG {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		return "", err
	}

	if v, ok := configm[name]; ok == true {
		switch v.(type) {
		case string:
			value = v.(string)
		case int:
			value = fmt.Sprintf("%d", v.(int))
		default:
			return "", fmt.Errorf("yaml value of unsupported type, only string and int types are supported")
		}
	}

	return value, nil
}

func YamlSet(name, value string) error {
	configf, err := os.Open(YamlConfigPath)
	if err == nil {
		configm := make(map[interface{}]interface{})
		err := yaml.NewDecoder(configf).Decode(&configm)
		if err != nil {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		configf.Close()
		configm[name] = value
		configf, err := os.Create(YamlConfigPath)
		if err == nil {
			defer configf.Close()
			confige := yaml.NewEncoder(configf)
			err := confige.Encode(configm)
			if err == nil {
				confige.Close()
				configf.Close()
			} else {
				log("WARNING yaml.Encoder.Encode: %v", err)
				return err
			}
		} else {
			log("WARNING os.Create config file %s: %v", YamlConfigPath, err)
			return err
		}
	} else {
		log("WARNING os.Open config file %s: %v", YamlConfigPath, err)
		return err
	}

	return nil
}

func KvGet(name string) (value string, err error) {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		nil,
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("kv api response status: %s", resp.Status)
	}

	if rbb, err := io.ReadAll(resp.Body); err != nil {
		return "", err
	} else {
		value = string(rbb)
	}

	return value, nil
}

func KvSet(name, value string) error {
	mpbb := new(bytes.Buffer)
	mpw := multipart.NewWriter(mpbb)
	if err := mpw.WriteField("metadata", "{}"); err != nil {
		return err
	}
	if err := mpw.WriteField("value", value); err != nil {
		return err
	}
	mpw.Close()

	req, err := http.NewRequest(
		"PUT",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		mpbb,
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("kv api response status: %s", resp.Status)
	}

	return nil
}

func init() {
	var err error

	if os.Getenv("YamlConfigPath") != "" {
		YamlConfigPath = os.Getenv("YamlConfigPath")
	}
	if YamlConfigPath == "" {
		log("WARNING YamlConfigPath empty")
	}

	KvToken, err = GetVar("KvToken")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvToken == "" {
		log("WARNING KvToken empty")
	}

	KvAccountId, err = GetVar("KvAccountId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvAccountId == "" {
		log("WARNING KvAccountId empty")
	}

	KvNamespaceId, err = GetVar("KvNamespaceId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvNamespaceId == "" {
		log("WARNING KvNamespaceId empty")
	}

	TgToken, err = GetVar("TgToken")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	TgChatId, err = GetVar("TgChatId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgChatId == "" {
		log("ERROR TgChatId empty")
		os.Exit(1)
	}

	TgBossChatId, err = GetVar("TgBossChatId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgBossChatId == "" {
		log("ERROR TgBossChatId empty")
		os.Exit(1)
	}

	YtKey, err = GetVar("YtKey")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtKey == "" {
		log("ERROR: YtKey empty")
		os.Exit(1)
	}

	YtUsername, err = GetVar("YtUsername")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtChannelId, err = GetVar("YtChannelId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtPublishedAfter, err = GetVar("YtPublishedAfter")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
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

func ytsearch() (*youtube.Video, error) {
	var err error

	ytsvc, err := youtube.NewService(context.TODO(), option.WithAPIKey(YtKey))
	if err != nil {
		return nil, fmt.Errorf("NewService: %w", err)
	}

	// https://developers.google.com/youtube/v3/docs/search/list

	s := ytsvc.Search.List([]string{"id", "snippet"}).MaxResults(1).Order("date").Type("video")
	s = s.ChannelId(YtChannelId).EventType(YtEventType)
	s = s.PublishedAfter(YtPublishedAfter)
	rs, err := s.Do()
	if err != nil {
		return nil, fmt.Errorf("search/list: %w", err)
	}

	if DEBUG {
		log("search.list:")
		log("response: %+v", rs)
		log("items:%d", len(rs.Items))
		for i, item := range rs.Items {
			log("n:%03d id:%s title:`%s`", i+1, item.Id.VideoId, item.Snippet.Title)
		}
	}

	if len(rs.Items) == 0 {
		return nil, nil
	}

	if DEBUG {
		log("videos.list:")
	}

	vid := rs.Items[0].Id.VideoId

	// https://developers.google.com/youtube/v3/docs/videos/list

	v := ytsvc.Videos.List([]string{"snippet", "liveStreamingDetails"})
	v = v.Id(vid)
	rv, err := v.Do()
	if err != nil {
		return nil, fmt.Errorf("videos/list: %w", err)
	}
	if DEBUG {
		log("response: %+v", rv)
	}
	if len(rv.Items) == 0 {
		return nil, fmt.Errorf("video %s not found", vid)
	}

	var item *youtube.Video
	item = rv.Items[0]

	return item, nil
}

func tgpost(item *youtube.Video) error {
	var err error

	sstime, err := time.Parse(time.RFC3339, item.LiveStreamingDetails.ScheduledStartTime)
	if err != nil {
		return fmt.Errorf("parse start time: %w", err)
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

	if DEBUG {
		log("published at: %s"+NL, item.Snippet.PublishedAt)
	}

	if DEBUG {
		log("thumbnail: %s"+NL, thumbnailurl)
	}

	tzmoscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return fmt.Errorf("time.LoadLocation: %w", err)
	}
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

	if DEBUG {
		log("%s"+NL, caption)
	}

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
		return fmt.Errorf("telegram send photo: %w", err)
	}

	log("posted tg message id:%s"+NL, msg.Id)

	return nil
}

func main() {
	var err error

	var item *youtube.Video
	item, err = ytsearch()
	if err != nil {
		log("youtube search: %s", err)
		tglog("youtube search: %s", err)
		os.Exit(1)
	}
	if item == nil {
		log("no %s events", YtEventType)
		tglog("no %s events", YtEventType)
		os.Exit(0)
	}

	err = tgpost(item)
	if err != nil {
		log("telegram post: %s", err)
		tglog("telegram post: %s", err)
		os.Exit(1)
	}

	patime, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
	if err != nil {
		log("parse PublishedAt time: %s", err)
		tglog("parse PublishedAt time: %s", err)
		os.Exit(1)
	}

	patime = patime.Add(1 * time.Second)
	if DEBUG {
		log("published after: %s"+NL, patime.Format(time.RFC3339))
	}

	err = SetVar(YtPublishedAfter, patime.Format(time.RFC3339))
	if err != nil {
		log("SetVar YtPublishedAfter: %s", err)
		tglog("SetVar YtPublishedAfter: %s", err)
		os.Exit(1)
	}

}
