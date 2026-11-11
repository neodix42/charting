package trading

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxTradeScreenBytes = 32 * 1024 * 1024

type TradeScreen struct {
	ID            string `json:"id"`
	TradeID       string `json:"tradeId"`
	Provider      string `json:"provider"`
	Environment   string `json:"environment"`
	Account       string `json:"account"`
	OrderID       string `json:"orderId"`
	Instrument    string `json:"instrument"`
	Market        string `json:"market"`
	Direction     string `json:"direction"`
	TradeOpenDate int64  `json:"tradeOpenDate"`
	CreatedAt     int64  `json:"createdAt"`
	FileName      string `json:"fileName"`
	MIMEType      string `json:"mimeType"`
	Bytes         int64  `json:"bytes"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SHA256        string `json:"sha256"`
}

type TradeScreenQuery struct {
	TradeID        string `json:"tradeId"`
	Provider       string `json:"provider"`
	Instrument     string `json:"instrument"`
	OrderID        string `json:"orderId"`
	Environment    string `json:"environment"`
	EntryCondition string `json:"entryCondition"`
	From           int64  `json:"from"`
	To             int64  `json:"to"`
	Offset         int    `json:"offset"`
	Limit          int    `json:"limit"`
}
type TradeScreenPage struct {
	Screens []TradeScreen `json:"screens"`
	Total   int           `json:"total"`
	Offset  int           `json:"offset"`
	Limit   int           `json:"limit"`
}

func screenExtension(mime string) string {
	return map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/bmp": ".bmp"}[mime]
}
func validTradeScreen(s TradeScreen, tradeID string) bool {
	hash, err := hex.DecodeString(s.SHA256)
	provider, _, _ := strings.Cut(tradeID, ":")
	return ValidIntent(s.ID) && s.TradeID == tradeID && s.Provider == provider && screenExtension(s.MIMEType) != "" && s.Bytes > 0 && s.Bytes <= MaxTradeScreenBytes && s.CreatedAt > 0 && s.Width > 0 && s.Height > 0 && len(s.FileName) <= 255 && !strings.ContainsAny(s.FileName, "/\\\r\n") && err == nil && len(hash) == sha256.Size
}

func decodeTradeScreen(dataURL string) ([]byte, string, int, int, error) {
	header, encoded, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") || len(header) > 128 || len(encoded) > base64.StdEncoding.EncodedLen(MaxTradeScreenBytes) {
		return nil, "", 0, 0, errors.New("Choose a PNG, JPEG, GIF, WebP or BMP image up to 32 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > MaxTradeScreenBytes {
		return nil, "", 0, 0, errors.New("Invalid or oversized image")
	}
	mime := http.DetectContentType(data)
	if screenExtension(mime) == "" {
		return nil, "", 0, 0, errors.New("Supported images: PNG, JPEG, GIF, WebP and BMP")
	}
	width, height := 0, 0
	if mime == "image/bmp" && len(data) >= 26 {
		width, height = int(int32(binary.LittleEndian.Uint32(data[18:22]))), int(int32(binary.LittleEndian.Uint32(data[22:26])))
		if height < 0 {
			height = -height
		}
	} else if mime == "image/webp" && len(data) >= 25 {
		switch string(data[12:16]) {
		case "VP8X":
			if len(data) >= 30 {
				width = 1 + int(data[24]) + (int(data[25]) << 8) + (int(data[26]) << 16)
				height = 1 + int(data[27]) + (int(data[28]) << 8) + (int(data[29]) << 16)
			}
		case "VP8L":
			if data[20] == 0x2f {
				bits := binary.LittleEndian.Uint32(data[21:25])
				width = 1 + int(bits&0x3fff)
				height = 1 + int((bits>>14)&0x3fff)
			}
		case "VP8 ":
			if len(data) >= 30 && bytes.Equal(data[23:26], []byte{0x9d, 0x01, 0x2a}) {
				width = int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
				height = int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
			}
		}
	} else if config, _, e := image.DecodeConfig(bytes.NewReader(data)); e == nil {
		width, height = config.Width, config.Height
	}
	if width <= 0 || height <= 0 {
		return nil, "", 0, 0, errors.New("The image header or dimensions are invalid")
	}
	return data, mime, width, height, nil
}

func (s *EntryConditions) screenDirectory(tradeID string, create bool) (string, error) {
	if !validAnnotatedTrade(tradeID) {
		return "", errors.New("Invalid trade identity")
	}
	provider, intent, _ := strings.Cut(tradeID, ":")
	directory := filepath.Dir(s.path)
	for _, part := range []string{"trade-screens", provider, intent} {
		directory = filepath.Join(directory, part)
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) && create {
			if err = os.Mkdir(directory, 0700); err != nil {
				return "", err
			}
			info, err = os.Lstat(directory)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Screen folders must be real directories")
		}
	}
	return directory, nil
}

func (s *EntryConditions) AddScreen(trade Trade, fileName, dataURL string) (TradeScreen, error) {
	var screen TradeScreen
	if !validAnnotatedTrade(trade.ID) {
		return screen, errors.New("Invalid trade identity")
	}
	data, mime, width, height, err := decodeTradeScreen(dataURL)
	if err != nil {
		return screen, err
	}
	fileName = path.Base(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "." || fileName == ".." || fileName == "/" || fileName == "" {
		fileName = "Screenshot" + screenExtension(mime)
	}
	if !utf8.ValidString(fileName) || len(fileName) > 255 || strings.ContainsAny(fileName, "\r\n") {
		return screen, errors.New("Invalid image filename")
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return screen, err
	}
	hash := sha256.Sum256(data)
	provider, _, _ := strings.Cut(trade.ID, ":")
	screen = TradeScreen{ID: hex.EncodeToString(id[:]), TradeID: trade.ID, Provider: provider, Environment: trade.Environment, Account: trade.Account, OrderID: trade.OrderID, Instrument: trade.Instrument, Market: trade.Market, Direction: trade.Direction, TradeOpenDate: trade.OpenDate, CreatedAt: time.Now().UnixMilli(), FileName: fileName, MIMEType: mime, Bytes: int64(len(data)), Width: width, Height: height, SHA256: hex.EncodeToString(hash[:])}
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, err := s.read()
	if err != nil {
		return TradeScreen{}, err
	}
	if _, exists := metadata.Trades[trade.ID]; !exists && len(metadata.Trades) >= 20000 {
		return TradeScreen{}, errors.New("Trade annotations exceed 20,000 records; original file preserved")
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return TradeScreen{}, err
	}
	directory, err := s.screenDirectory(trade.ID, true)
	if err != nil {
		return TradeScreen{}, err
	}
	filePath := filepath.Join(directory, screen.ID+screenExtension(mime))
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return TradeScreen{}, err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		_ = os.Remove(filePath)
		return TradeScreen{}, err
	}
	if directoryFile, openErr := os.Open(directory); openErr == nil {
		_ = directoryFile.Sync()
		_ = directoryFile.Close()
	}
	note := metadata.Trades[trade.ID]
	note.Screens = append(note.Screens, screen)
	metadata.Trades[trade.ID] = note
	metadata.Version = 2
	if err = WriteJournal(s.path, metadata, false); err != nil {
		_ = os.Remove(filePath)
		return TradeScreen{}, err
	}
	return screen, nil
}

func (s *EntryConditions) ReadScreen(tradeID, screenID string) (string, error) {
	if !validAnnotatedTrade(tradeID) || !ValidIntent(screenID) {
		return "", errors.New("Invalid trade screen identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, err := s.read()
	if err != nil {
		return "", err
	}
	for _, screen := range metadata.Trades[tradeID].Screens {
		if screen.ID != screenID {
			continue
		}
		directory, err := s.screenDirectory(tradeID, false)
		if err != nil {
			return "", err
		}
		p := filepath.Join(directory, screen.ID+screenExtension(screen.MIMEType))
		info, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() != screen.Bytes || info.Size() > MaxTradeScreenBytes {
			return "", errors.New("Saved image is missing or changed")
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != screen.SHA256 {
			return "", errors.New("Saved image checksum mismatch; original file preserved")
		}
		return "data:" + screen.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	}
	return "", errors.New("This screen does not belong to the selected trade")
}

func (s *EntryConditions) QueryScreens(query TradeScreenQuery) (TradeScreenPage, error) {
	out := TradeScreenPage{Screens: []TradeScreen{}, Offset: query.Offset, Limit: query.Limit}
	if out.Limit == 0 {
		out.Limit = 100
	}
	if query.Offset < 0 || query.Offset > 20000 || out.Limit < 1 || out.Limit > 500 || query.From < 0 || query.To < 0 || query.From > 0 && query.To > 0 && query.From > query.To || query.TradeID != "" && !validAnnotatedTrade(query.TradeID) || len(query.Instrument) > 128 || len(query.OrderID) > 128 || len(query.EntryCondition) > 400 {
		return out, errors.New("Invalid screen query")
	}
	metadata, err := s.MetadataSnapshot()
	if err != nil {
		return out, err
	}
	var rows []TradeScreen
	for id, note := range metadata {
		if query.TradeID != "" && id != query.TradeID || query.EntryCondition != "" && !strings.Contains(strings.ToLower(note.EntryCondition), strings.ToLower(query.EntryCondition)) {
			continue
		}
		for _, screen := range note.Screens {
			if query.Provider != "" && screen.Provider != query.Provider || query.Instrument != "" && screen.Instrument != query.Instrument || query.OrderID != "" && screen.OrderID != query.OrderID || query.Environment != "" && screen.Environment != query.Environment || query.From > 0 && screen.CreatedAt < query.From || query.To > 0 && screen.CreatedAt >= query.To {
				continue
			}
			rows = append(rows, screen)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt != rows[j].CreatedAt {
			return rows[i].CreatedAt > rows[j].CreatedAt
		}
		return rows[i].ID < rows[j].ID
	})
	out.Total = len(rows)
	start := min(query.Offset, len(rows))
	out.Screens = append(out.Screens, rows[start:min(start+out.Limit, len(rows))]...)
	return out, nil
}
