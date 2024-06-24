package whatsappbot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"database/sql"

	"github.com/febriliankr/whatsapp-cloud-api"
	_ "github.com/lib/pq"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const (
	whatsAppServer = "s.whatsapp.net"
	sayMenu        = "For a command list please type & send-: menu?\nPlease include the question mark."

	reminderGreeting = "Please save your email address, by typing & sending-: update email: example@emailprovider.com"

	coldGreeting = "Hello there, I don't believe we've met before."

	smartyPantsGreeting = "Hey there smarty pants, I see you've been here before."

	noCommandText = "Err:NC, Sorry I couldn't identify a command in your mesasge."

	unhandledCommandException = "Err:CF, Something went wrong processing your request."

	updateOrderCommand = `update order 1:newAmount, 3:newAmount, 2:newAmount, ...
where 1, 2 or 3 is the item number as listed in the price list - item order not important.

For items with options please use the format-: 1x3, 3x1, 2x2, ...
The first number is the option's hierarchical menu position and the second is your desired amount of that option.`

	fullOrderExample = `An order of: 
12 grams of Peanut butter breath, 
3 Blue dream cannisters, 
2 Slurricane cannister,
1 GMO cannisters and 
5 grams of Strawberry cheesecake.

Should look like-: update order 9:12, 10: 1x3, 3x2, 2x1, 6:5`

	prclstPreamble = `Welcome to Flying Rasta,

to save your order please type & send-:` + updateOrderCommand + "\n\n" + fullOrderExample + ` 

To checkout type & send-: checkoutnow?`

	mainMenu = `Main Menu, command list:

fr.prlist? - Prints the Flying Rasta price list.

menu? - Prints this menu.
userinfo? - Prints your user info.
currentorder? - Prints your current pending order.
checkoutnow? - Prints a payment link for your current basket.

update email: newEmail
update nickname: newNickname
update social: newSocial
update consent: newConsent` + "\n\n" + updateOrderCommand
)

type Command interface {
	Execute(db *sql.DB, ui UserInfo, isAutoInc bool) error
}

type CommandCollection []Command

type CommandData struct {
	Name string
	Text string
}

type UpdateUserInfoCommand struct {
	CommandData
}

type UpdateOrderCommand struct {
	CommandData
}

type QuestionCommand struct {
	CommandData
}

type ChatClient struct {
	*whatsmeow.Client
	*whatsapp.Whatsapp
}

type Chat interface {
	SendMessage(client ChatClient, destinationNum, chatMessage string) error
}

func (c *ChatClient) SendMessage(destinationNum, chatMessage string) error {
	if c.Client != nil {
		jId := types.NewJID(destinationNum, whatsAppServer)
		_, err := c.Client.SendMessage(context.Background(), jId, &waProto.Message{Conversation: proto.String(chatMessage)})
		if err != nil {
			log.Printf("ReturnToUser Failed with: " + err.Error())
			return fmt.Errorf("ReturnToUser Failed with: " + err.Error())
		}
		return nil
	} else if c.Whatsapp != nil {
		_, err := c.SendText(destinationNum, chatMessage)
		if err != nil {
			log.Println("ReturnToUser Failed with: " + err.Error())
			return fmt.Errorf("ReturnToUser Failed with: " + err.Error())
		}
		return nil
	} else {
		return errors.New("WhatsApp client object not instantiated")
	}
}

func (cmd UpdateUserInfoCommand) Execute(db *sql.DB, ui UserInfo, isAutoInc bool) error {
	var colName = strings.TrimSpace(strings.TrimPrefix(cmd.Name, "update"))
	err := ui.UpdateSingularUserInfoField(db, colName, cmd.Text)
	if err != nil {
		return fmt.Errorf("unhandled error updating user info: %v", err)
	}
	return errors.New("successfully updated user info." + colName + " to " + cmd.Text)
}

func (cmd UpdateOrderCommand) Execute(db *sql.DB, ui UserInfo, isAutoInc bool) error {
	updates, err := ParseUpdateOrderCommand(cmd.Text)
	if err != nil {
		return fmt.Errorf("error parsing update answers command: %v", err)
	}
	// Create a new Order struct
	curOrder := CustomerOrder{
		CellNumber:  ui.CellNumber,
		CatalogueID: catalogueID,
		OrderItems:  OrderItems{MenuIndications: updates},
	}
	err = curOrder.UpdateOrInsertCurrentOrder(db, ui.CellNumber, catalogueID, curOrder.OrderItems, isAutoInc)
	if err != nil {
		return fmt.Errorf("unhandled error updating order: %v", err)
	}
	return errors.New("successfully updated current order")
}

func (cmd QuestionCommand) Execute(db *sql.DB, ui UserInfo, isAutoInc bool) error {
	return fmt.Errorf("%s", cmd.CommandData.Text)
}

func BeginCheckout(db *sql.DB, ui UserInfo, c CustomerOrder, checkoutUrls CheckoutInfo, isAutoInc bool) string {

	// Create a new URL object for each URL
	returnURL, _ := url.Parse(checkoutUrls.ReturnURL)
	cancelURL, _ := url.Parse(checkoutUrls.CancelURL)
	notifyURL, _ := url.Parse(checkoutUrls.NotifyURL)

	// Initialize checkoutURLs with the new URLs
	checkoutUrls.ReturnURL = returnURL.String()
	checkoutUrls.CancelURL = cancelURL.String()
	checkoutUrls.NotifyURL = notifyURL.String()

	//Tally the order and then create a CheckoutCart struct
	cartTotal, cartSummary, err := c.TallyOrder(db, ui.CellNumber, isAutoInc)
	if err != nil {
		return err.Error()
	}
	cart := CheckoutCart{
		ItemName:      c.BuildItemName(checkoutUrls.ItemNamePrefix),
		CartTotal:     cartTotal,
		OrderID:       c.OrderID,
		CustFirstName: ui.NickName.String,
		CustLastName:  ui.CellNumber,
		CustEmail:     ui.Email.String}
	return cartSummary + "/n/n" + ProcessPayment(cart, checkoutUrls)
}

func parseQuestionCommand(match string, ui UserInfo, c CustomerOrder, db *sql.DB, checkoutUrls CheckoutInfo, isAutoInc bool) Command {
	switch match {
	case "currentorder?":
		return QuestionCommand{CommandData: CommandData{Name: "currentorder", Text: c.GetCurrentOrderAsAString(db, ui.CellNumber, isAutoInc)}}
	case "fr.prlist?":
		return QuestionCommand{CommandData: CommandData{Name: "fr.prlist", Text: prclstPreamble + "\n\n" + PriceListAsAString()}}
	case "userinfo?":
		return QuestionCommand{CommandData: CommandData{Name: "userinfo", Text: ui.GetUserInfoAsAString()}}
	case "checkoutnow?":
		return QuestionCommand{CommandData: CommandData{Name: "checkoutnow", Text: BeginCheckout(db, ui, c, checkoutUrls, isAutoInc)}}
	default:
		return QuestionCommand{CommandData: CommandData{Name: "menu", Text: mainMenu}}
	}
}

func (cc CommandCollection) ProcessCommands(ui UserInfo, db *sql.DB, isAutoInc bool) string {
	var errors []string
	for _, command := range cc {
		err := command.Execute(db, ui, isAutoInc)
		if err != nil {
			errors = append(errors, err.Error())
		}
	}
	return strings.Join(errors, "\n")
}

func (c *ChatClient) ChatBegin(convo ConversationContext, db *sql.DB, checkoutUrls CheckoutInfo, isAutoInc bool) {
	commandRes := unhandledCommandException
	commands := GetCommandsFromLastMessage(convo.MessageBody, convo, db, checkoutUrls, isAutoInc)
	if len(commands) != 0 {
		// Process commands
		commandRes_Temp := CommandCollection(commands).ProcessCommands(convo.UserInfo, db, isAutoInc)
		if commandRes_Temp != "" && commandRes_Temp != " " && commandRes_Temp != "\n" {
			commandRes = commandRes_Temp
		}
	} else {
		commandRes = noCommandText
	}

	if !convo.UserExisted {
		if commandRes != noCommandText {
			commandRes = smartyPantsGreeting + "\n\n" + commandRes + "\n\n" + reminderGreeting + "\n\n" + sayMenu
		} else {
			commandRes = coldGreeting + "\n\n" + reminderGreeting + "\n\n" + sayMenu
		}
	} else if commandRes == noCommandText {
		commandRes += "\n\n" + sayMenu
	}

	convo.UserExisted = true

	// Main - Send a WhatsApp response
	err := c.SendMessage(convo.UserInfo.CellNumber, commandRes)
	if err != nil {
		log.Println(err.Error())
		return
	}
}

// Precompile regular expressions
var (
	regexQuestionMark  = regexp.MustCompile(`(menu\?|fr\.prlist\?|userinfo\?|currentorder\?|checkoutnow\?)`)
	regexUpdateField   = regexp.MustCompile(`(update email|update nickname|update social|update consent):\s*(\S*)`)
	regexUpdateAnswers = regexp.MustCompile(`(update order):?\s*(.*)`)
)

func GetCommandsFromLastMessage(messageBody string, convo ConversationContext, db *sql.DB, checkoutUrls CheckoutInfo, isAutoInc bool) []Command {
	var commands []Command
	messageBody = strings.ToLower(messageBody)

	// Use precompiled regular expressions
	if matches := regexQuestionMark.FindAllStringSubmatch(messageBody, -1); matches != nil {
		for _, match := range matches {
			commands = append(commands, parseQuestionCommand(match[1], convo.UserInfo, convo.CurrentOrder, db, checkoutUrls, isAutoInc))
		}
	}

	if matches := regexUpdateField.FindAllStringSubmatch(messageBody, -1); matches != nil {
		for _, match := range matches {
			commands = append(commands, UpdateUserInfoCommand{CommandData: CommandData{Name: match[1], Text: match[2]}})
		}
	}

	if matches := regexUpdateAnswers.FindAllStringSubmatch(messageBody, -1); matches != nil {
		for _, match := range matches {
			commands = append(commands, UpdateOrderCommand{CommandData: CommandData{Name: match[1], Text: match[2]}})
		}
	}

	return commands
}

func ParseUpdateOrderCommand(commandText string) ([]MenuIndication, error) {
	// Remove "update order" prefix
	commandText = strings.TrimPrefix(commandText, "update order")
	commandText = strings.TrimPrefix(commandText, ":")
	commandText = strings.TrimSpace(commandText)
	commandText = strings.Replace(commandText, " ", "", 1)

	// Regular expression to match "ItemMenuNum: ItemAmount" pairs
	re := regexp.MustCompile(`\b\d+:\s*(?:\d+x\d+(?:,\s*)?)+`)

	// Find all matches in the commandText
	matches := re.FindAllString(commandText, -1)

	// Remove matched parts from the commandText
	for k, match := range matches {
		trimmedMatch := strings.TrimSpace(match)
		trimmedMatch = strings.TrimSuffix(trimmedMatch, ",")
		matches[k] = trimmedMatch
		commandText = strings.Replace(commandText, match, "", 1)
	}

	// Trim any remaining whitespace or commas
	commandText = strings.Trim(commandText, ",")

	// Initialize slice to store OrderItems
	var orderItems []MenuIndication

	// Process each match
	for _, match := range matches {
		orderItem, err := parseOrderItem(match)
		if err != nil {
			return nil, err
		}
		orderItems = append(orderItems, orderItem)
	}

	// Process remaining commandText for simple "ItemMenuNum: ItemAmount" pairs
	if commandText != "" {
		remainingItems := strings.Split(commandText, ",")
		for _, item := range remainingItems {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			orderItem, err := parseOrderItem(item)
			if err != nil {
				return nil, err
			}
			orderItems = append(orderItems, orderItem)
		}
	}

	return orderItems, nil
}

func parseOrderItem(item string) (MenuIndication, error) {
	parts := strings.SplitN(item, ":", 2)
	if len(parts) != 2 {
		return MenuIndication{}, fmt.Errorf("failed to parse item: %s", item)
	}

	// Parse ItemMenuNum
	itemMenuNum, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return MenuIndication{}, fmt.Errorf("failed to parse ItemMenuNum: %v", err)
	}

	// Trim and clean up ItemAmount
	itemAmount := strings.TrimSpace(parts[1])
	itemAmount = strings.TrimSuffix(itemAmount, ",")

	return MenuIndication{
		ItemMenuNum: itemMenuNum,
		ItemAmount:  itemAmount,
	}, nil
}
