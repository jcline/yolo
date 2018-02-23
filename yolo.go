package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"

	homedir "github.com/mitchellh/go-homedir"
)

func getConfigDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".yolomail"), nil
}

func getMailDir() (string, error) {
	home, err := getConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, "mail"), nil
}

func mkMailDir() error {
	d, err := getMailDir()
	if err != nil {
		return err
	}

	return os.MkdirAll(d, 0700)
}

func createConfigDir() error {
	dir, err := getConfigDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0700)
}

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func getClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile, err := tokenCacheFile()
	if err != nil {
		log.Fatalf("Unable to get path to cached credential file. %v", err)
	}
	tok, err := tokenFromFile(cacheFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok)
}

// getTokenFromWeb uses Config to request a Token.
// It returns the retrieved Token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}

// tokenCacheFile generates credential file path/filename.
// It returns the generated credential path/filename.
func tokenCacheFile() (string, error) {
	conf, err := getConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(conf,
		url.QueryEscape("token.json")), err
}

// tokenFromFile retrieves a Token from a given file path.
// It returns the retrieved Token and any read error encountered.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	defer f.Close()
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

type fuckyou struct {
}

func (f fuckyou) Get() (string, string) {
	return "format", "RAW"
}

type tokenOpt struct {
	token string
}

func (t tokenOpt) Get() (string, string) {
	return "pageToken", t.token
}

type spamTrashOpt struct {
}

func (s spamTrashOpt) Get() (string, string) {
	return "includeSpamTrash", "true"
}

type pretty struct {
}

func (p pretty) Get() (string, string) {
	return "pretty", "false"
}

type msgid struct {
	id string
}

func (m msgid) Get() (string, string) {
	return "id", m.id
}

func buildExtantMap(path string) (map[string]struct{}, error) {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}

	extant := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		extant[file.Name()] = struct{}{}
	}

	return extant, nil
}

func dumpAllMessages(srv *gmail.Service, mdir string) error {
	extant, err := buildExtantMap(mdir)
	if err != nil {
		return err
	}

	list, err := srv.Users.Messages.List("me").Do(fuckyou{}, spamTrashOpt{}, pretty{})
	if err != nil {
		return err
	}

	for {
		token := tokenOpt{
			token: list.NextPageToken,
		}

		// Our queries cost 5, we get 250 points/second, this should
		// prevent us going over 50 pts/s
		time.Sleep(10 * time.Millisecond)

		for _, msg := range list.Messages {
			log.Println(msg.Id)
			if _, ok := extant[msg.Id]; ok {
				log.Println("Found already seen message... stopping!")
				return nil
			}

			realmsg, err := srv.Users.Messages.Get("me", msg.Id).Do(fuckyou{}, spamTrashOpt{}, pretty{})
			if err != nil {
				return err
			}

			time.Sleep(10 * time.Millisecond)
			b, err := base64.URLEncoding.DecodeString(realmsg.Raw)
			if err != nil {
				log.Fatalf("Failed to decode mail! %v\n%v", err, msg)
				return err
			}
			err = ioutil.WriteFile(filepath.Join(mdir, msg.Id), b, 0644)
			if err != nil {
				log.Fatalf("Failed to write mail! %v\n%v", err, msg)
				return err
			}
		}

		if len(token.token) == 0 {
			break
		}

		list, err = srv.Users.Messages.List("me").Do(fuckyou{}, spamTrashOpt{}, pretty{}, token)
		if err != nil {
			return err
		}

	}
	return nil
}

func main() {
	err := createConfigDir()
	if err != nil {
		log.Fatalf("Unable to create config dir: %v", err)
	}

	configDir, err := getConfigDir()
	if err != nil {
		log.Fatalf("Unable to load config dir: %v", err)
	}

	ctx := context.Background()

	b, err := ioutil.ReadFile(filepath.Join(configDir, "client_secret.json"))
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete your previously saved credentials
	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(ctx, config)

	srv, err := gmail.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve gmail Client %v", err)
	}

	userName := "me"
	r, err := srv.Users.Labels.List(userName).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve labels. %v", err)
	}
	if len(r.Labels) > 0 {
		fmt.Print("Labels:\n")
		for _, l := range r.Labels {
			fmt.Printf("- %s\n", l.Name)
		}
	} else {
		fmt.Print("No labels found.")
	}

	mpath, err := getMailDir()
	if err != nil {
		log.Fatalf("Unable to load mail dir: %v", err)
	}
	err = mkMailDir()
	if err != nil {
		log.Fatalf("Failed to make mail dir: %v", err)
	}
	fmt.Println(mpath)

	err = dumpAllMessages(srv, mpath)
	if err != nil {
		log.Fatalf("Failed to dump all mail: %v", err)
	}
}
