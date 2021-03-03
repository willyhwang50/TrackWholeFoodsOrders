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
	"regexp"
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

// Indicators for extracting features.
const (
	GrandTotal   = "Grand total:"
	DeliveryTime = "delivery time:"
	OrderID      = "Details Order"
)

// String Longform used for Parsing Strings to type Time
const (
	longform  = "Jan 2, 2006 at 3:04pm (MST)"
	shortform = "2006-Jan-02"
)

const query = "{from:order-update@amazon.com} 'your delivery is complete' 'Grand total'"

// Monthmap connects abbreviated name of month to corresponding int
var Monthmap = map[string]string{
	"Jan": "01",
	"Feb": "02",
	"Mar": "03",
	"Apr": "04",
	"May": "05",
	"Jun": "06",
	"Jul": "07",
	"Aug": "08",
	"Sep": "09",
	"Oct": "10",
	"Nov": "11",
	"Dec": "12",
}

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

// Order TYPE has properties ID, Date, Total.
type Order struct {
	OrdNum     string
	OrdDate    string
	GrandTotal float64
}

//GetSummary of the properties.
func (order Order) GetSummary() {
	fmt.Println(order.OrdNum, order.OrdDate, order.GrandTotal)
}

// GetOrdDate changes OrdDate in Stringform to time.Time
func (order Order) GetOrdDate() time.Time {
	date, err := time.Parse(shortform, order.OrdDate)
	if err != nil {
		log.Fatal("cannot convert OrdDate to time.Time", err)
	}
	return date
}

// ConvtoTime casts dates written in strings to type time.Time.
func ConvtoTime(t string) string {
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
	shortdate := year + "-" + Monthmap[month] + "-" + day
	return shortdate
}

// ExtractFeat finds the order Id, Date, Grand Total given email body as string.
func ExtractFeat(s *[]byte) (id string, date string, tot float64) {
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

// GetOrderFeats creates an array of Order structs.
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

// InsertOrder to db.
func InsertOrder(Orders []Order, db *sql.DB) {
	for i, ord := range Orders {
		id := ord.OrdNum
		DateString := ord.OrdDate
		total := fmt.Sprintf("%f", ord.GrandTotal)
		values := "'" + id + "', '" + DateString + "', " + total
		sql := "INSERT INTO WholeFoods(order_id, order_date, grand_total) VALUES" +
			"(" + values + ")"
		_, err := db.Exec(sql)
		if err != nil {
			log.Printf("cannot add %d th row to database", i)
			log.Fatal(err)
		}
	}
}

// GetQuery forms a SQL Query using lastupdate
func GetQuery(query string, lastupdate string) string {
	re := regexp.MustCompile("-")
	lastupdate = re.ReplaceAllString(lastupdate, "/")
	re = regexp.MustCompile("[A-Za-z]+")
	lastupdate = re.ReplaceAllString(lastupdate, Monthmap[lastupdate[5:8]])
	NewQuery := query + " after:" + lastupdate
	return NewQuery
}

// ReadData retrieves relevant Emails.
func ReadData(srv *gmail.Service, lastupdate string) []Order {
	const user = "me"
	NewQuery := GetQuery(query, lastupdate)
	r, err := srv.Users.Messages.List(user).IncludeSpamTrash(false).MaxResults(10).Q(NewQuery).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve mails: %v", err)
	}
	fmt.Println("The length of the list is", len(r.Messages))

	//extract features from emails
	Orders := GetOrderFeats(user, srv, r)
	if len(Orders) == 0 {
		log.Fatal("Orders is empty")
	}

	//Save Newly extracted Data as temporary json file
	Ordfile, err := json.Marshal(Orders)
	if err != nil {
		log.Fatal("Can't marshall json", err)
	}
	ioutil.WriteFile("Orders.json", Ordfile, os.ModePerm)

	return Orders
}

//UpdateDB with new records after the lastupdate date
func UpdateDB(srv *gmail.Service, lastupdate string, db *sql.DB) {

	// Retrieve Data from Email Address
	Orders := ReadData(srv, lastupdate)

	//insert data into db
	InsertOrder(Orders, db)
	fmt.Println("successfully uploaded data")
}

//RetrieveOrders from mySQL Database.
func RetrieveOrders(db *sql.DB, cond *Conditions) *[]Order {
	var OrdData []Order
	sql := (*cond).GetQuery()
	fmt.Println(sql)
	rows, err := db.Query(sql)
	if err != nil {
		log.Fatal("cannot load data from mySQL database")
	}
	defer rows.Close()

	for rows.Next() {
		var ord Order
		var id string
		err = rows.Scan(&id, &ord.OrdNum, &ord.OrdDate, &ord.GrandTotal)
		if err != nil {
			log.Fatal("cannot retrieve data", err)
		}
		fmt.Print(id)
		ord.GetSummary()
		OrdData = append(OrdData, ord)
	}
	return &OrdData
}

// Conditions is a struct which contains information for writing a select query
type Conditions struct {
	start   string
	end     string
	lb      string
	ub      string
	numrows string
}

// GetNumRows Retrieves the property numrows as float64
func (c Conditions) GetNumRows() int {
	nr, err := strconv.Atoi(c.numrows)
	if err != nil {
		log.Fatal("cannot retrieve numrows as int", err)
	}
	return nr
}

// GetConditions retrieves summary of the conditions as a string
func (c Conditions) GetConditions() string {
	cond := "Date: " + c.start + "~" + c.end + "/ "
	cond += "Total amount: " + c.lb + " ~ " + c.ub + "/ "
	cond += "Number of Rows: " + c.numrows + "/ "
	return cond
}

//GetQuery function creates a Query for retrieving data from mySQL
func (c Conditions) GetQuery() string {
	Query := "SELECT * from Wholefoods where '" + c.start + "' < order_date and order_date < '" + c.end + "' and " + c.lb + " < grand_total and grand_total < " + c.ub + " limit " + c.numrows
	return Query
}

//GetSumQuery writes a Query that retrieves a summary statistics
func (c Conditions) GetSumQuery() string {
	subquery := c.GetQuery()
	Query := "SELECT DATEDIFF(max(t1.order_date), min(t1.order_date)) as gap, avg(t1.grand_total) as spending from (" + subquery + ") t1;"
	return Query
}

//CreateView collects conditions for desired data
func CreateView(srv *gmail.Service, db *sql.DB) {
	Condmap := map[int]bool{
		1: false,
		2: false,
		3: false,
	}
	condInit := Conditions{
		start:   "2021-01-01",
		end:     "2021-05-01",
		lb:      "0.0",
		ub:      "100000",
		numrows: "100",
	}
	cond := condInit
	var cat int
CondPanel:
	for {
		fmt.Println("Specify conditions for the data you want to view: ")
		fmt.Println("Current Conditions are: ", cond.GetConditions())
		fmt.Println("Add Conditions of...")
		fmt.Println("1. Date")
		fmt.Println("2. Total Amount")
		fmt.Println("3. Number of Rows")
		fmt.Println("4. Retrieve All data")
		fmt.Println("5. Retrieve With Current Condition")
		fmt.Println("6. Return to Main")
		fmt.Scanln(&cat)
		switch cat {
		case 1:
			if Condmap[1] {
				var resp string
				fmt.Println("Dates Already Specified. Do you want to Override? yes/no")
				fmt.Scanln(&resp)
				if resp == "no" {
					continue
				}
			}
			var start string
			var end string
			fmt.Println("Enter Starting Date: (format yyyy-mm-dd)")
			fmt.Scanln(&start)
			cond.start = start
			fmt.Println("Enter End Date: (format yyyy-mm-dd)")
			fmt.Scanln(&end)
			cond.end = end
			Condmap[1] = true
		case 2:
			if Condmap[2] {
				var resp string
				fmt.Println("Total Amount Already Specified. Do you want to Override? yes/no")
				fmt.Scanln(&resp)
				if resp == "no" {
					continue
				}
			}
			var lb string
			var ub string
			fmt.Println("1. Greater Than")
			fmt.Scanln(&lb)
			cond.lb = lb
			fmt.Println("2. Less Than")
			fmt.Scanln(&ub)
			cond.ub = ub
			Condmap[2] = true
		case 3:
			if Condmap[3] {
				var resp string
				fmt.Println("Number of Rows Already Specified. Do you want to Override? yes/no")
				fmt.Scanln(&resp)
				if resp == "no" {
					continue
				}
			}
			var numrows string
			fmt.Println("How Many Rows do you want?")
			fmt.Scanln(&numrows)
			cond.numrows = numrows
			Condmap[3] = true
		case 4:
			fmt.Println("Retrieving All data")
			_ = RetrieveOrders(db, &condInit)
		case 5:
			fmt.Println("Retrieving Data with conditions: ", cond.GetConditions())
			_ = RetrieveOrders(db, &cond)
			break CondPanel
		case 6:
			break CondPanel
		default:
			fmt.Println("Not a valid category")
			continue
		}
	}
	return
}

// ShowPattern summarizes purchase patterns
func ShowPattern(db *sql.DB, cond *Conditions) {
	SumQuery := cond.GetSumQuery()
	rows, err := db.Query(SumQuery)
	if err != nil {
		log.Fatal("cannot get summary data", err)
	}
	var gap int
	var spending float64
	for rows.Next() {
		rows.Scan(&gap, &spending)
		fmt.Println(gap)
		fmt.Println(spending)
	}
	AvgGap := gap / cond.GetNumRows()
	AvgSpend := spending
	fmt.Printf("You are purchasing every %d days \n", AvgGap)
	fmt.Printf("You are spending about %f $s per order \n", AvgSpend)
}

// CreateStats based on the Database.
func CreateStats(db *sql.DB) {
	CondStats := Conditions{
		start:   "2021-01-01",
		end:     "2021-05-01",
		lb:      "0.0",
		ub:      "100000",
		numrows: "7",
	}
	fmt.Println("What do you want to do?")
	fmt.Println("1. Summarize Purchase Pattern")
	fmt.Println("2. Predict next order date. (amount fixed)")
	fmt.Println("3. Predict how much I should order (date fixed)")
	fmt.Println("4. Return to main menu")
	var stats int
	fmt.Scanln(&stats)
StatsQuery:
	for {
		switch stats {
		case 1:
			ShowPattern(db, &CondStats)
			break StatsQuery
		case 2:
			//PredictDate(db)
			break StatsQuery
		case 3:
			//PredictAmt(db)
			break StatsQuery
		case 4:
			break StatsQuery
		default:
			fmt.Println("Not a Valid input. Try again.")
			fmt.Scanln(&stats)
		}
	}

}

func main() {
	// Welcome Message
	fmt.Println("Welcome! Initiating...")

	//Read Gmail Credientials
	fmt.Println("Reading Credentials...")
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}
	fmt.Println("Creating Client...")

	// Create a new client using credentials
	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	//instantiate a gmail service
	fmt.Println("Instantiating Gmail Service...")
	srv, err := gmail.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}

	//Get Environmental Variables
	fmt.Println("Loading Environmental Variables...")
	godotenv.Load(".env")

	//retrieve db password
	password := os.Getenv("password")
	if len(password) == 0 {
		log.Fatal("cannot load password")
	}

	//Instantiate Database Driver
	fmt.Println("Connecting to mySQL Database")
	db, err := sql.Open("mysql", "root:"+password+"@tcp(127.0.0.1:3306)/sys")
	if err != nil {
		log.Fatal("cannot instantaite database driver", err)
	}

	//Check if Database should be Updated
	LastUpdate, found := os.LookupEnv("LastUpdate")
	if !found {
		LastUpdate = "2021-Jan-01"
		os.Setenv("LastUpdate", LastUpdate)
	}
	fmt.Println("Your last update is on ", LastUpdate)
	LastUpdateTime, err := time.Parse(shortform, LastUpdate)
	if err != nil {
		log.Fatal("cannot parse lastupdate date to time")
	}
	TimeNow := time.Now()
	if !LastUpdateTime.Equal(TimeNow) {
		fmt.Println("You have not updated your database for", TimeNow.Sub(LastUpdateTime).Hours(), "hours")
	}
	fmt.Print("Do you want to update your database?: yes/no (lowercase)")
	var update string
	fmt.Scanln(&update)

UpdateQ:
	for {
		switch update {
		case "yes":
			UpdateDB(srv, LastUpdate, db)
			os.Setenv("LastUpdate", LastUpdate)
			fmt.Println("Update is complete. Latest Update is now", LastUpdate)
			break UpdateQ
		case "no":
			fmt.Println("Not Updating Database. Latest Update is", LastUpdate)
			break UpdateQ
		default:
			fmt.Println("Not a proper command. Type 'yes' or 'no'")
			fmt.Scanln(&update)
		}
	}

	fmt.Println("Directing you to Control Panel")
	fmt.Println("...........................................................")

	var action int
ActionPanel:
	for {
		//Choose Action
		fmt.Println("Choose Options: ")
		fmt.Println("1: View Order Records")
		fmt.Println("2: Edit Order Records")
		fmt.Println("3: Get Stats")
		fmt.Println("4: Quit")
		fmt.Scanln(&action)
		switch action {
		case 1:
			fmt.Println("Directing to View...")
			CreateView(srv, db)
		case 2:
			fmt.Println("Directing to Edit...")
			//CreateEdit(srv, db)
		case 3:
			fmt.Println("Directing to Stats...")
			CreateStats(db)
		case 4:
			fmt.Println("Bye bye")
			break ActionPanel
		default:
			fmt.Println("Not a valid choice")
			continue
		}
	}
}

// Update Once then view directs to db
// unmarshall json file (temp)
