package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image/png"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Telegram accepts photos up to 10 MB, a width plus height of at most 10000,
// and an aspect ratio of at most 20. Keep the byte bound conservative.
const maxPhotoBytes = 10_000_000

// SendAlertPhoto uploads an in-memory PNG with the complete plain-text signal.
// An uncertain upload is never retried or replaced by a text notification.
func (s *Store) SendAlertPhoto(ctx context.Context, message string, image []byte) error {
	return s.SendAlertWithChart(ctx, message, image, "")
}

// SendAlertWithChart sends a chart, or explicitly reports rendering failure in
// the text notification when image is nil. chartError must be a sanitized,
// user-facing reason, never a raw error containing paths, credentials or URLs.
// A successful text-only fallback returns chartUnavailable with confirmed
// recipient counts so callers can count delivery and expose the missing image.
func (s *Store) SendAlertWithChart(ctx context.Context, message string, image []byte, chartError string) error {
	if !utf8.ValidString(message) || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 4096 {
		return fail("message")
	}
	var send recipientSender
	fallback := len(image) == 0 && strings.TrimSpace(chartError) != ""
	if fallback {
		notice := chartUnavailableNotice(chartError)
		parts := []string{message + "\n\n" + notice}
		if utf8.RuneCountInString(parts[0]) > 4096 {
			parts = []string{message, notice}
		}
		send = func(ctx context.Context, config configuration, recipient string, bot BotIdentity, id int64, messageID *int64) (bool, error) {
			confirmed := false
			for _, part := range parts {
				if err := s.sendRecipient(ctx, config, recipient, bot, id, part, messageID); err != nil {
					return confirmed, err
				}
				confirmed = true
			}
			return confirmed, nil
		}
	} else {
		if chartError != "" || !validPhoto(image) {
			return fail("photo")
		}
		// Retain our own immutable payload through a queued multi-recipient send.
		image = bytes.Clone(image)
		caption := message
		separateText := captionUnits(message) > 1024
		if separateText {
			caption = "CryptoDesk signal chart. Full signal details follow."
		}
		send = func(ctx context.Context, config configuration, recipient string, bot BotIdentity, id int64, messageID *int64) (bool, error) {
			err := s.sendVerified(ctx, config, recipient, bot, id, messageID, func(sent *sentMessage) error {
				return s.client.sendPhoto(ctx, config.APIToken, id, caption, image, sent)
			})
			if err != nil {
				return false, err
			}
			if separateText {
				return true, s.sendRecipient(ctx, config, recipient, bot, id, message, messageID)
			}
			return true, nil
		}
	}
	result, err := s.sendBatch(ctx, true, true, send)
	if err != nil {
		return fail("storage")
	}
	if fallback && result.Sent {
		result.Sent, result.Code = false, "chartUnavailable"
	}
	if !result.Sent {
		if result.TotalRecipients > 0 {
			return &DeliveryError{ConfirmedCount: result.SentCount, TotalRecipients: result.TotalRecipients, Code: result.Code}
		}
		return fail(result.Code)
	}
	return nil
}

// Plain-text captions have no entities. Count supplementary Unicode scalars as
// two UTF-16 units, which safely respects Telegram's caption/entity limits.
func captionUnits(text string) int {
	units := 0
	for _, r := range text {
		units++
		if r > 0xffff {
			units++
		}
	}
	return units
}

func chartUnavailableNotice(reason string) string {
	var safe strings.Builder
	count := 0
	for _, r := range strings.ToValidUTF8(reason, "") {
		if unicode.IsControl(r) {
			r = ' '
		}
		if count == 160 {
			break
		}
		safe.WriteRune(r)
		count++
	}
	notice := "Chart image unavailable for this signal."
	if text := strings.TrimSpace(safe.String()); text != "" {
		notice += " " + text
	}
	return notice
}

func (c *client) sendPhoto(ctx context.Context, token string, id int64, caption string, image []byte, output *sentMessage) error {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if form.WriteField("chat_id", strconv.FormatInt(id, 10)) != nil || form.WriteField("caption", caption) != nil {
		return fail("photo")
	}
	part, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="photo"; filename="signal-chart.png"`},
		"Content-Type":        {"image/png"},
	})
	if err != nil {
		return fail("photo")
	}
	if _, err = part.Write(image); err != nil || form.Close() != nil {
		return fail("photo")
	}
	var sent struct {
		sentMessage
		Photo []struct {
			FileID string `json:"file_id"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"photo"`
	}
	if err = c.request(ctx, token, "sendPhoto", form.FormDataContentType(), body.Bytes(), &sent); err != nil {
		return err
	}
	if len(sent.Photo) == 0 {
		return fail("uncertain")
	}
	for _, photo := range sent.Photo {
		if photo.FileID == "" || photo.Width <= 0 || photo.Height <= 0 {
			return fail("uncertain")
		}
	}
	*output = sent.sentMessage
	return nil
}

func validPhoto(image []byte) bool {
	if len(image) < 8 || len(image) > maxPhotoBytes || !bytes.Equal(image[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	config, err := png.DecodeConfig(bytes.NewReader(image))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width+config.Height > 10000 || config.Width > config.Height*20 || config.Height > config.Width*20 {
		return false
	}
	// Check the complete bounded chunk stream, including CRCs and terminal IEND,
	// without allocating a potentially large decoded image. The chart renderer
	// owns pixel generation; this rejects truncated or corrupted upload payloads.
	idat := false
	for offset := 8; offset < len(image); {
		if len(image)-offset < 12 {
			return false
		}
		length := int(binary.BigEndian.Uint32(image[offset : offset+4]))
		if length > len(image)-offset-12 {
			return false
		}
		end := offset + 8 + length
		if crc32.ChecksumIEEE(image[offset+4:end]) != binary.BigEndian.Uint32(image[end:end+4]) {
			return false
		}
		kind := string(image[offset+4 : offset+8])
		if kind == "IDAT" {
			idat = true
		}
		if kind == "IEND" {
			return length == 0 && idat && end+4 == len(image)
		}
		offset = end + 4
	}
	return false
}
