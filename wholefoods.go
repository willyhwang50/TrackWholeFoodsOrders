package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	_ "github.com/go-sql-driver/mysql"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// GrandTotal Indicator.
const GrandTotal string = "Grand total:"

// DeliveryTime Indicator.
const DeliveryTime string = "delivery time:"

// OrderID Indicator.
const OrderID string = "Details Order"

// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config) *http.Client {
	// The file token.json stores the user's access and refresh tokens, and is
	// created automatically when the authorization flow completes for the first
	// time.
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

// Order has properties ID, Date, Total.
type Order struct {
	OrdNum     string
	OrdDate    time.Time
	GrandTotal float64
}

//ConvtoTime string to type time.Time.
func ConvtoTime(t string) time.Time {
	const shortForm = "2006-Jan-02"
	rawtime := strings.Fields(t)[1:]
	month := rawtime[0][:3]
	day := strings.Trim(rawtime[1], ",")
	dayint, err := strconv.Atoi(day)
	if err != nil {
		log.Fatal("cannot convert day to int", err)
	}
	if dayint < 10 {
		day = "0" + day
	}
	year := rawtime[2]
	shortdate := year + "-" + month + "-" + day
	ordtime, err := time.Parse(shortForm, shortdate)
	if err != nil {
		log.Fatal("cannot convert to time obj", err)
	}
	return ordtime
}

// ExtractFeat Id, Date, Grand Total given email body as string.
func ExtractFeat(s *[]byte) (id string, date time.Time, tot float64) {
	info := strings.Fields(string(*s))
	var idFound bool = false
	var dtFound bool = false
	var totFound bool = false
	for i := 0; i < len(info)-20; i++ {
		if !dtFound && strings.Join(info[i:i+2], " ") == DeliveryTime {
			rawdate := strings.Join(info[i+2:i+2+4], " ")
			date = ConvtoTime(rawdate)
			dtFound = true
		} else if !totFound && strings.Join(info[i:i+2], " ") == GrandTotal {
			trim := strings.Trim(info[i+2], "$")
			total, err := strconv.ParseFloat(trim, 64)
			if err != nil {
				log.Fatal("cannot convert Grand Total to float", err)
			}
			if total <= 0 {
				log.Fatal("cannot convert Grand Total to float")
			} else {
				tot = total
			}
			totFound = true
		} else if !idFound && strings.Join(info[i:i+2], " ") == OrderID {
			id = info[i+2]
			idFound = true
		}
	}
	return id, date, tot
}

// GetOrderFeats message response to create an array of Order struct.
func GetOrderFeats(user string, srv *gmail.Service, r *gmail.ListMessagesResponse) []Order {
	Orders := []Order{}
	for _, msg := range r.Messages {
		RawMsg, err := srv.Users.Messages.Get(user, msg.Id).Do()
		if err != nil {
			log.Fatalf("Message %v not retrieved. Moving on to the next message", err)
		}
		BodyMsg := RawMsg.Payload.Parts[0].Body.Data
		StrBody, err := base64.URLEncoding.DecodeString(BodyMsg)
		if err != nil {
			log.Fatalf("Message %v not retrieved. Moving on to the next message", err)
		}
		id, date, tot := ExtractFeat(&StrBody)
		fmt.Println(id, date, tot)
		Orders = append(Orders, Order{OrdNum: id, OrdDate: date, GrandTotal: tot})
	}
	return Orders
}

// GetPassword to db.
func GetPassword(key string) string {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("env not loaded", err)
	}
	return os.Getenv(key)
}

// InsertOrder to db.
func InsertOrder(Orders []Order, db *sql.DB) {
	for i, ord := range Orders {
		id := ord.OrdNum
		yearnum, month, daynum := ord.OrdDate.Date()
		year := strconv.Itoa(yearnum)
		day := strconv.Itoa(daynum)
		DateString := year + "-" + month.String() + "-" + day
		total := fmt.Sprintf("%f", ord.GrandTotal)
		values := "'" + id + "', '" + DateString + "', " + total
		sql := "INSERT INTO WholeFoods(order_id, order_date, grand_total) VALUES" +
			"(" + values + ")"
		_, err := db.Exec(sql)
		if err != nil {
			log.Fatal("cannot add %d th row to database", i, err)
		}
	}
}

func main() {
	//read credientials
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// create a new client using credentials
	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	//instantiate a gmail service
	srv, err := gmail.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}

	//retrieve mails
	user := "me"
	r, err := srv.Users.Messages.List(user).IncludeSpamTrash(
		false).MaxResults(10).Q("{from:order-update@amazon.com} 'your delivery is complete' 'Grand total'").Do()
	if err != nil {
		log.Fatalf("Unable to retrieve mails: %v", err)
	}
	fmt.Println(len(r.Messages))

	//extract features from emails
	Orders := GetOrderFeats(user, srv, r)
	if len(Orders) == 0 {
		log.Fatal("Orders is empty")
	}

	//marshall array of orders into json
	Ordfile, err := json.Marshal(Orders)
	if err != nil {
		log.Fatal("Can't marshall json", err)
	}

	//save json file for later
	ioutil.WriteFile("Orders.json", Ordfile, os.ModePerm)

	//retrieve db password
	password := GetPassword("password")
	if len(password) == 0 {
		log.Fatal("cannot load password")
	}

	//Instantiate Database Driver
	db, err := sql.Open("mysql", "root:"+password+"@tcp(127.0.0.1:3306)/sys")
	if err != nil {
		log.Fatal(err)
	}

	//insert data into db
	InsertOrder(Orders, db)
	fmt.Println("successfully uploaded data")
}
