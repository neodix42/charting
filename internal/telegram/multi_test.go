package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCommaRecipientsNormalizeValidateAndPreserveFailedEdits(t *testing.T) {
	s := NewStore(t.TempDir())
	got, err := s.Configure("123:test", " +00042 , @Alice, @alice,42 , 84 ")
	if err != nil || got.Recipient != "42, @Alice, 84" {
		t.Fatalf("normalization: %+v %v", got, err)
	}
	before, _ := os.ReadFile(s.path)
	values := []string{}
	for i := 1; i <= 21; i++ {
		values = append(values, fmt.Sprint(i))
	}
	for _, bad := range []string{",42", "42,", "42,,84", "42, @bad name", "@Alice, -1", strings.Join(values, ","), strings.Repeat(" ", 4097)} {
		if _, err = s.Configure("456:new", bad); err == nil {
			t.Fatalf("invalid list accepted: %q", bad)
		}
		after, _ := os.ReadFile(s.path)
		if string(after) != string(before) {
			t.Fatal("invalid edit replaced previous settings")
		}
	}
	if _, err = s.Configure("123:test", strings.Join(values[:20], ",")); err != nil {
		t.Fatal(err)
	}
	if got, err = NewStore(filepath.Dir(s.path)).Read(); err != nil || len(recipientValues(got.Recipient)) != 20 {
		t.Fatal(got, err)
	}
}

func multiClient(t *testing.T, status func(int64) int) (*Store, *[]int64, *int) {
	t.Helper()
	sent := []int64{}
	updates := 0
	var store *Store
	store = setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
		name := func(id int64) string {
			if id == 42 {
				return "Alice"
			}
			return "Bob"
		}
		switch method {
		case "getMe":
			return response(200, botResponse())
		case "getWebhookInfo":
			return response(200, map[string]any{"url": ""})
		case "getUpdates":
			updates++
			return response(200, []any{map[string]any{"message": map[string]any{"chat": privateChat(42, "Alice")}}, map[string]any{"message": map[string]any{"chat": privateChat(84, "Bob")}}, map[string]any{"message": map[string]any{"chat": privateChat(99, "Stranger"), "text": "/start"}}})
		case "getChat":
			id := int64(input["chat_id"].(float64))
			if id == 99 {
				t.Fatal("unlisted sender was treated as a notification recipient")
			}
			return response(200, privateChat(id, name(id)))
		case "sendMessage":
			id := int64(input["chat_id"].(float64))
			sent = append(sent, id)
			// The transport only removes wall-clock waiting; pacing/cancel behavior has
			// independent production tests, and all recipient logic remains unchanged.
			store.nextSend = time.Time{}
			if code := status(id); code != 200 {
				return response(code, nil)
			}
			return response(200, map[string]any{"message_id": len(sent), "chat": privateChat(id, name(id))})
		}
		t.Fatalf("unexpected API method %s", method)
		return nil, nil
	})
	return store, &sent, &updates
}

func TestMultipleNamesDeliverIndependentlyAndReuseVerifiedMappingsAfterRestart(t *testing.T) {
	s, sent, updates := multiClient(t, func(int64) int { return 200 })
	if _, err := s.Configure("123:test", "@Alice, @Bob"); err != nil {
		t.Fatal(err)
	}
	result, err := s.Test(context.Background())
	if err != nil || !result.Sent || result.SentCount != 2 || result.TotalRecipients != 2 || !reflect.DeepEqual(*sent, []int64{42, 84}) {
		t.Fatalf("fanout: %+v %v sends=%v", result, err, *sent)
	}
	calls := *updates
	restored := NewStore(filepath.Dir(s.path))
	restored.client = s.client
	if result, err = restored.Test(context.Background()); err != nil || !result.Sent || result.SentCount != 2 || *updates != calls {
		t.Fatalf("restart resolution: %+v %v updatecalls=%d", result, err, *updates)
	}
	config, err := restored.readLocked()
	if err != nil || len(config.Resolved) != 2 {
		t.Fatal(config, err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil || strings.Contains(string(data), "Stranger") || strings.Contains(string(data), `"chatId":99`) {
		t.Fatalf("unlisted sender was saved as a subscriber: %s %v", data, err)
	}
	if _, err = restored.Configure("123:test", "@BOB, 42"); err != nil {
		t.Fatal(err)
	}
	config, err = restored.readLocked()
	if err != nil || len(config.Resolved) != 1 || config.Resolved[0].Recipient != "@BOB" {
		t.Fatalf("mapping pruning: %+v %v", config, err)
	}
	if _, err = restored.Save("456:replacement"); err != nil {
		t.Fatal(err)
	}
	config, err = restored.readLocked()
	if err != nil || len(config.Resolved) != 0 || config.ChatID != 0 || config.BotID != 0 {
		t.Fatalf("token change retained identity: %+v %v", config, err)
	}
}

func TestResolvedAliasesSendOnceAndPersistEveryConfiguredIdentity(t *testing.T) {
	s, sent, updates := multiClient(t, func(int64) int { return 200 })
	if _, err := s.Configure("123:test", "42, @Alice, 84, @Bob"); err != nil {
		t.Fatal(err)
	}
	result, err := s.Test(context.Background())
	if err != nil || !result.Sent || result.SentCount != 2 || result.TotalRecipients != 2 || !reflect.DeepEqual(*sent, []int64{42, 84}) {
		t.Fatalf("alias dedup: %+v %v sends=%v", result, err, *sent)
	}
	calls := *updates
	restored := NewStore(filepath.Dir(s.path))
	restored.client = s.client
	if result, err = restored.Test(context.Background()); err != nil || !result.Sent || result.TotalRecipients != 2 || *updates != calls {
		t.Fatalf("alias cache lost: %+v %v calls=%d", result, err, *updates)
	}
}

func TestIndividualFailureContinuesOtherRecipientsAndAlertReportsPartialCounts(t *testing.T) {
	s, sent, _ := multiClient(t, func(id int64) int {
		if id == 42 {
			return 403
		}
		return 200
	})
	_, _ = s.Configure("123:test", "42, 84")
	result, err := s.Test(context.Background())
	if err != nil || result.Sent || result.Code != "blocked" || result.SentCount != 1 || result.TotalRecipients != 2 || !reflect.DeepEqual(*sent, []int64{42, 84}) {
		t.Fatalf("partial test: %+v %v sends=%v", result, err, *sent)
	}
	*sent = nil
	err = s.SendAlert(context.Background(), "Closed bar signal")
	if err == nil || !strings.Contains(err.Error(), "1 of 2") || !strings.Contains(err.Error(), "blocked") || strings.Contains(err.Error(), "123:test") || !reflect.DeepEqual(*sent, []int64{42, 84}) {
		t.Fatalf("partial alert summary: %v sends=%v", err, *sent)
	}
}

func TestGlobalFailureStopsRemainingRecipientsAndOverridesEarlierIndividualFailure(t *testing.T) {
	for _, code := range []int{401, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s, sent, _ := multiClient(t, func(id int64) int {
				if id == 42 {
					return 403
				}
				return code
			})
			_, _ = s.Configure("123:test", "42, 84, 126")
			result, err := s.Test(context.Background())
			want := map[int]string{401: "auth", 429: "rateLimit", 500: "uncertain"}[code]
			if err != nil || result.Sent || result.Code != want || result.TotalRecipients != 3 || !reflect.DeepEqual(*sent, []int64{42, 84}) {
				t.Fatalf("global error: %+v %v sends=%v", result, err, *sent)
			}
		})
	}
}

func TestFanoutCancellationAndSettingsChangeStopBeforeAnotherDelivery(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(fmt.Sprint(change), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var s *Store
			sends := 0
			s = setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
				switch method {
				case "getMe":
					return response(200, botResponse())
				case "getChat":
					id := int64(input["chat_id"].(float64))
					if id == 84 {
						if change {
							if _, err := s.Configure("123:test", "126"); err != nil {
								t.Fatal(err)
							}
						} else {
							cancel()
						}
					}
					return response(200, privateChat(id, "user"))
				case "sendMessage":
					sends++
					s.nextSend = time.Time{}
					return response(200, map[string]any{"message_id": sends, "chat": privateChat(int64(input["chat_id"].(float64)), "user")})
				}
				return nil, errors.New("unexpected method")
			})
			_, _ = s.Configure("123:test", "42, 84")
			result, err := s.Test(ctx)
			want := "network"
			if change {
				want = "changed"
			}
			if err != nil || result.Sent || result.Code != want || result.SentCount != 1 || result.TotalRecipients != 2 || sends != 1 {
				t.Fatalf("interrupted batch: %+v %v sends=%d", result, err, sends)
			}
		})
	}
}

func TestLegacyRecipientMappingMigratesWhenAnotherRecipientIsAdded(t *testing.T) {
	s := NewStore(t.TempDir())
	original := []byte(`{"apiToken":"123:test","recipient":"@Alice","resolvedChatId":42,"resolvedBotId":10}`)
	if err := os.WriteFile(s.path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Configure("123:test", "@Alice, @Bob"); err != nil {
		t.Fatal(err)
	}
	config, err := s.readLocked()
	if err != nil || len(config.Resolved) != 1 || config.Resolved[0].ChatID != 42 || config.ChatID != 0 {
		t.Fatalf("legacy migration: %+v %v", config, err)
	}
	before, _ := os.ReadFile(s.path)
	var document map[string]any
	_ = json.Unmarshal(before, &document)
	document["resolvedRecipients"] = []any{map[string]any{"recipient": "@someone_else", "chatId": 42, "botId": 10}}
	corrupt, _ := json.Marshal(document)
	_ = os.WriteFile(s.path, corrupt, 0600)
	if _, err = s.Read(); err == nil {
		t.Fatal("unselected mapping accepted")
	}
	if _, err = s.Configure("new:token", "42"); err == nil {
		t.Fatal("corrupt mapping overwritten")
	}
	after, _ := os.ReadFile(s.path)
	if string(after) != string(corrupt) {
		t.Fatal("corrupt mapping was changed")
	}
}
