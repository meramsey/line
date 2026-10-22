package connector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type loginCommandIntent struct {
	bridgev2.MatrixAPI
	replies []string
}

func (i *loginCommandIntent) SendMessage(_ context.Context, _ id.RoomID, _ event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	i.replies = append(i.replies, content.AsMessage().Body)
	return &mautrix.RespSendEvent{}, nil
}

type loginCommandNetwork struct {
	LineConnector
	chosen string
}

func (n *loginCommandNetwork) CreateLogin(_ context.Context, _ *bridgev2.User, flow string) (bridgev2.LoginProcess, error) {
	n.chosen = flow
	return nil, errors.New("test authentication dispatch")
}

func TestLineLoginCommand(t *testing.T) {
	for _, tc := range []struct {
		name, input, chosen, reply string
		qr, denied                 bool
	}{
		{name: "default help", qr: true, reply: "login qr"},
		{name: "explicit help", input: "HeLp", qr: true, reply: "login email"},
		{name: "single flow help", reply: "login email"},
		{name: "qr alias", input: "qR", qr: true, chosen: LoginFlowIDQR},
		{name: "email alias", input: "EMAIL", chosen: LoginFlowIDEmail},
		{name: "qr ID", input: strings.ToUpper(LoginFlowIDQR), qr: true, chosen: LoginFlowIDQR},
		{name: "email ID", input: LoginFlowIDEmail, chosen: LoginFlowIDEmail},
		{name: "unknown", input: "password", reply: "Unknown or unavailable"},
		{name: "disabled qr", input: "qr", reply: "Unknown or unavailable"},
		{name: "permission", input: "email", denied: true, reply: "do not have permissions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			network := &loginCommandNetwork{LineConnector: LineConnector{Config: Config{QRLogin: tc.qr}}}
			bot := &loginCommandIntent{}
			br := &bridgev2.Bridge{Network: network, Config: &bridgeconfig.BridgeConfig{CommandPrefix: "!line"}}
			ce := &commands.Event{Ctx: context.Background(), Bridge: br, Bot: bot, Command: "login", Args: strings.Fields(tc.input), User: &bridgev2.User{User: &database.User{}, Permissions: bridgeconfig.Permissions{Login: !tc.denied}}}
			lineLoginCommand().Run(ce)
			if network.chosen != tc.chosen {
				t.Fatalf("dispatched %q, want %q", network.chosen, tc.chosen)
			}
			if len(bot.replies) != 1 || !strings.Contains(bot.replies[0], tc.reply) {
				t.Fatalf("replies = %v, want %q", bot.replies, tc.reply)
			}
			if !tc.qr && strings.Contains(bot.replies[0], "login qr") {
				t.Fatal("help advertised disabled QR flow")
			}
		})
	}
}
