package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type photoRequest struct {
	method      string
	values      map[string]any
	image       []byte
	filename    string
	contentType string
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func photoResult(id int64) map[string]any {
	return map[string]any{"message_id": 7, "chat": privateChat(id, "neodix"), "photo": []any{map[string]any{"file_id": "opaque-photo-id", "width": 4, "height": 3}}}
}

// Every request uses this synthetic transport, including identity verification;
// no configured production credentials, network or external messages are used.
func setupPhotoClient(t *testing.T, handle func(photoRequest) (*http.Response, error)) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	store.client = newClient(fakeTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "api.telegram.org" || request.Method != http.MethodPost {
			t.Fatalf("unsafe Telegram destination: %s", request.URL.Redacted())
		}
		input := photoRequest{method: request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:], values: map[string]any{}}
		if input.method == "sendPhoto" {
			reader, err := request.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				if part.FormName() == "photo" {
					if input.image != nil {
						t.Fatal("multiple photo parts")
					}
					input.image, input.filename, input.contentType = data, part.FileName(), part.Header.Get("Content-Type")
				} else {
					input.values[part.FormName()] = string(data)
				}
			}
			id, err := strconv.ParseInt(input.values["chat_id"].(string), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			input.values["chat_id"] = float64(id)
		} else if err := json.NewDecoder(request.Body).Decode(&input.values); err != nil {
			t.Fatal(err)
		}
		if isWrite(input.method) {
			// Keep synthetic tests fast without changing production pacing. The
			// sender owns both sendGate and settings mutex during this callback.
			store.nextSend = time.Time{}
		}
		if answer, err := handle(input); answer != nil || err != nil {
			return answer, err
		}
		switch input.method {
		case "getMe":
			return response(200, botResponse())
		case "getChat":
			return response(200, privateChat(int64(input.values["chat_id"].(float64)), "neodix"))
		case "sendPhoto":
			return response(200, photoResult(int64(input.values["chat_id"].(float64))))
		case "sendMessage":
			return response(200, map[string]any{"message_id": 8, "chat": privateChat(int64(input.values["chat_id"].(float64)), "neodix")})
		}
		t.Fatalf("unexpected method: %s", input.method)
		return nil, nil
	}))
	if _, err := store.Configure("123:test", "42"); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPhotoSignalUploadsExactPNGAndPlainUnicodeCaption(t *testing.T) {
	chart := testPNG(t)
	message := "CryptoDesk Live Alert\nBTC/USDC — Цена < SMA & RSI > 40 📈\nEntry: 1.1\nSL: 1\nTP: 1.3"
	var methods []string
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		methods = append(methods, input.method)
		if input.method == "sendPhoto" {
			if !bytes.Equal(input.image, chart) || input.filename != "signal-chart.png" || input.contentType != "image/png" || len(input.values) != 2 || input.values["caption"] != message || input.values["chat_id"] != float64(42) {
				t.Fatalf("changed upload or enabled formatting: %+v", input)
			}
		}
		return nil, nil
	})
	if err := store.SendAlertPhoto(context.Background(), message, chart); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "getMe,getChat,sendPhoto" {
		t.Fatal("unexpected requests", methods)
	}
}

func TestPhotoCaptionBoundaryPreservesEverySignalCharacter(t *testing.T) {
	for _, test := range []struct {
		name     string
		message  string
		separate bool
	}{
		{"ASCII limit", strings.Repeat("x", 1024), false},
		{"ASCII overflow", strings.Repeat("x", 1025), true},
		{"emoji limit", strings.Repeat("📈", 512), false},
		{"emoji overflow", strings.Repeat("📈", 513), true},
		{"full signal", strings.Repeat("Ж", 4096), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes []photoRequest
			store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
				if isWrite(input.method) {
					writes = append(writes, input)
				}
				return nil, nil
			})
			if err := store.SendAlertPhoto(context.Background(), test.message, testPNG(t)); err != nil {
				t.Fatal(err)
			}
			if test.separate {
				if len(writes) != 2 || writes[0].method != "sendPhoto" || captionUnits(writes[0].values["caption"].(string)) > 1024 || writes[1].method != "sendMessage" || writes[1].values["text"] != test.message {
					t.Fatalf("full signal lost or duplicated: %+v", writes)
				}
			} else if len(writes) != 1 || writes[0].values["caption"] != test.message {
				t.Fatalf("short caption changed: %+v", writes)
			}
		})
	}
}

func TestPhotoRejectsMalformedAndOutOfBoundsPayloadBeforeRPC(t *testing.T) {
	good := testPNG(t)
	dimensions := func(width, height uint32) []byte {
		data := bytes.Clone(good)
		binary.BigEndian.PutUint32(data[16:20], width)
		binary.BigEndian.PutUint32(data[20:24], height)
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		return data
	}
	corrupted := bytes.Clone(good)
	corrupted[len(corrupted)-5] ^= 1
	cases := [][]byte{nil, {}, []byte("not PNG"), good[:33], good[:len(good)-1], append(bytes.Clone(good), 'x'), corrupted, make([]byte, maxPhotoBytes+1), dimensions(9000, 1001), dimensions(1000, 1), dimensions(1, 1000)}
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		t.Fatalf("invalid payload contacted Telegram: %s", input.method)
		return nil, nil
	})
	for index, data := range cases {
		if err := store.SendAlertPhoto(context.Background(), "signal", data); operationCode(err) != "photo" {
			t.Fatalf("payload %d accepted: %v", index, err)
		}
	}
	for _, text := range []string{"", " \n", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if err := store.SendAlertPhoto(context.Background(), text, good); operationCode(err) != "message" {
			t.Fatal("invalid signal accepted", err)
		}
	}
	if err := store.SendAlertWithChart(context.Background(), "signal", good, "renderer failure"); operationCode(err) != "photo" {
		t.Fatal("ambiguous fallback accepted", err)
	}
}

func TestPhotoFailuresAreSanitizedNeverRetriedAndNeverFallBack(t *testing.T) {
	for _, test := range []struct {
		name string
		code string
		send func() (*http.Response, error)
	}{
		{"blocked", "blocked", func() (*http.Response, error) { return response(403, nil) }},
		{"rate limited", "rateLimit", func() (*http.Response, error) { return response(429, nil) }},
		{"invalid photo", "photo", func() (*http.Response, error) { return response(400, nil) }},
		{"server failed", "uncertain", func() (*http.Response, error) { return response(500, nil) }},
		{"network", "uncertain", func() (*http.Response, error) { return nil, errors.New("123:test secret-provider-payload") }},
		{"malformed body", "uncertain", func() (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("secret-provider-payload"))}, nil
		}},
		{"wrong chat", "uncertain", func() (*http.Response, error) { return response(200, photoResult(43)) }},
		{"missing photo", "uncertain", func() (*http.Response, error) {
			return response(200, map[string]any{"message_id": 7, "chat": privateChat(42, "neodix")})
		}},
		{"redirect", "uncertain", func() (*http.Response, error) {
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://attacker.invalid/123:test"}}, Body: io.NopCloser(strings.NewReader(`{"ok":false}`))}, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sends := 0
			store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
				if isWrite(input.method) {
					sends++
					if input.method != "sendPhoto" {
						t.Fatal("failed photo automatically fell back to text")
					}
					return test.send()
				}
				return nil, nil
			})
			err := store.SendAlertPhoto(context.Background(), strings.Repeat("x", 2000), testPNG(t))
			var delivery *DeliveryError
			if !errors.As(err, &delivery) || delivery.Code != test.code || delivery.ConfirmedCount != 0 || delivery.TotalRecipients != 1 || sends != 1 || strings.Contains(err.Error(), "123:test") || strings.Contains(err.Error(), "secret-provider") {
				t.Fatalf("unsafe or incorrect delivery result: %v sends=%d", err, sends)
			}
		})
	}
}

func TestPhotoThenFailedFullTextRetainsOneConfirmedSignal(t *testing.T) {
	var methods []string
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		if isWrite(input.method) {
			methods = append(methods, input.method)
		}
		if input.method == "sendMessage" {
			return response(500, nil)
		}
		return nil, nil
	})
	err := store.SendAlertPhoto(context.Background(), strings.Repeat("x", 1025), testPNG(t))
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.ConfirmedRecipients() != 1 || delivery.TotalRecipients != 1 || delivery.Code != "uncertain" || strings.Join(methods, ",") != "sendPhoto,sendMessage" {
		t.Fatalf("photo confirmation lost or duplicated: %v %v", err, methods)
	}
}

func TestExplicitChartFailureDeliversFullTextWithVisibleBoundedReason(t *testing.T) {
	for _, length := range []int{20, 4096} {
		var writes []string
		store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
			if input.method == "sendPhoto" {
				t.Fatal("explicit renderer failure attempted an upload")
			}
			if input.method == "sendMessage" {
				writes = append(writes, input.values["text"].(string))
			}
			return nil, nil
		})
		message := strings.Repeat("Ж", length)
		err := store.SendAlertWithChart(context.Background(), message, nil, "Renderer unavailable.\n"+strings.Repeat("R", 300))
		var delivery *DeliveryError
		if !errors.As(err, &delivery) || delivery.Code != "chartUnavailable" || delivery.ConfirmedRecipients() != 1 || delivery.TotalRecipients != 1 || !strings.Contains(err.Error(), "without a chart image") {
			t.Fatal("fallback confirmation missing", err)
		}
		if len(writes) == 0 || !strings.HasPrefix(writes[0], message) || !strings.Contains(strings.Join(writes, "\n"), "Chart image unavailable for this signal. Renderer unavailable.") {
			t.Fatal("fallback lost original signal or reason", writes)
		}
		if length == 4096 && (len(writes) != 2 || writes[0] != message || len([]rune(writes[1])) > 210) {
			t.Fatal("fallback truncated full signal or failed to bound notice", writes)
		}
	}
}

func TestPhotoMultiRecipientsContinueOnlyIndividualFailures(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
		var ids []int64
		store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
			if input.method == "sendPhoto" {
				id := int64(input.values["chat_id"].(float64))
				ids = append(ids, id)
				if id == 43 {
					return response(status, nil)
				}
			}
			return nil, nil
		})
		_, _ = store.Configure("123:test", "42,43,44")
		err := store.SendAlertPhoto(context.Background(), "signal", testPNG(t))
		var delivery *DeliveryError
		wantCount, wantWrites := 1, 2
		if status == 403 {
			wantCount, wantWrites = 2, 3
		}
		if !errors.As(err, &delivery) || delivery.ConfirmedRecipients() != wantCount || delivery.TotalRecipients != 3 || len(ids) != wantWrites {
			t.Fatalf("incorrect partial result status%d: %v writes%v", status, err, ids)
		}
	}
}

func TestPhotoCancellationStopsFullTextAfterAcknowledgedUpload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		if input.method == "sendPhoto" {
			writes++
			cancel()
		}
		if input.method == "sendMessage" {
			t.Fatal("cancelled signal sent follow-up text")
		}
		return nil, nil
	})
	err := store.SendAlertPhoto(ctx, strings.Repeat("x", 1025), testPNG(t))
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.ConfirmedRecipients() != 1 || delivery.Code != "network" || writes != 1 {
		t.Fatalf("cancellation lost confirmation: %v writes%d", err, writes)
	}
}

func TestPhotoSettingsChangeBetweenRecipientsStopsOriginalBatch(t *testing.T) {
	var store *Store
	writes := 0
	store = setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		if input.method == "getChat" && input.values["chat_id"] == float64(43) {
			if _, err := store.Configure("456:new", "44"); err != nil {
				t.Fatal(err)
			}
		}
		if input.method == "sendPhoto" {
			writes++
		}
		return nil, nil
	})
	_, _ = store.Configure("123:test", "42,43")
	err := store.SendAlertPhoto(context.Background(), "signal", testPNG(t))
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.ConfirmedRecipients() != 1 || delivery.Code != "changed" || writes != 1 {
		t.Fatalf("settings edit did not fence original batch: %v writes%d", err, writes)
	}
}

func TestPhotoQueuedSendAndPacingHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		if input.method == "getChat" {
			cancel()
		}
		if isWrite(input.method) {
			t.Fatal("cancelled pacing sent photo")
		}
		return nil, nil
	})
	store.nextSend = time.Now().Add(time.Hour)
	if err := store.SendAlertPhoto(ctx, "signal", testPNG(t)); err == nil {
		t.Fatal("cancelled pacing succeeded")
	}
	store.sendGate <- struct{}{}
	defer func() { <-store.sendGate }()
	if err := store.SendAlertPhoto(ctx, "signal", testPNG(t)); err == nil {
		t.Fatal("cancelled queued upload succeeded")
	}
}

func TestPhotoBatchDeduplicatesVerifiedPrivateAliases(t *testing.T) {
	writes := 0
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		switch input.method {
		case "getWebhookInfo":
			return response(200, map[string]any{"url": ""})
		case "getUpdates":
			return response(200, []any{map[string]any{"message": map[string]any{"chat": privateChat(42, "neodix")}}})
		case "sendPhoto":
			writes++
		}
		return nil, nil
	})
	_, _ = store.Configure("123:test", "@neodix,42")
	if err := store.SendAlertPhoto(context.Background(), "signal", testPNG(t)); err != nil || writes != 1 {
		t.Fatalf("alias duplicate delivered twice: %v writes%d", err, writes)
	}
}

func TestPhotoAndFullTextStayTogetherWhilePlainAlertsQueue(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var writes []string
	store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		switch input.method {
		case "sendPhoto":
			writes = append(writes, "photo")
			close(entered)
			<-release
		case "sendMessage":
			writes = append(writes, input.values["text"].(string))
		}
		return nil, nil
	})
	longSignal := strings.Repeat("s", 1025)
	chart := testPNG(t)
	photoDone := make(chan error, 1)
	go func() { photoDone <- store.SendAlertPhoto(context.Background(), longSignal, chart) }()
	<-entered
	if result, err := store.Test(context.Background()); err != nil || result.Code != "busy" {
		t.Fatalf("Test entered active photo batch: %+v %v", result, err)
	}
	plainDone := make(chan error, 1)
	go func() { plainDone <- store.SendAlert(context.Background(), "next signal") }()
	close(release)
	if err := <-photoDone; err != nil {
		t.Fatal(err)
	}
	if err := <-plainDone; err != nil {
		t.Fatal(err)
	}
	if result, err := store.Test(context.Background()); err != nil || !result.Sent {
		t.Fatalf("plain Test failed after photo batch: %+v %v", result, err)
	}
	if len(writes) != 4 || writes[0] != "photo" || writes[1] != longSignal || writes[2] != "next signal" || writes[3] != TestMessage {
		t.Fatal("photo parts interleaved or Test changed", writes)
	}
}

func TestPhotoSettingsChangeBetweenPhotoAndFullTextRetainsConfirmation(t *testing.T) {
	var store *Store
	changed := make(chan error, 1)
	store = setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
		if input.method == "sendPhoto" {
			store.nextSend = time.Now().Add(100 * time.Millisecond)
			go func() {
				_, err := store.Configure("456:new", "43")
				changed <- err
			}()
		}
		if input.method == "sendMessage" {
			t.Error("changed settings allowed old signal's full text")
		}
		return nil, nil
	})
	err := store.SendAlertPhoto(context.Background(), strings.Repeat("s", 1025), testPNG(t))
	if changeErr := <-changed; changeErr != nil {
		t.Fatal(changeErr)
	}
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.ConfirmedRecipients() != 1 || delivery.TotalRecipients != 1 || delivery.Code != "changed" {
		t.Fatal("settings change discarded photo confirmation", err)
	}
}
