package upload

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

// catboxEndpoint is catbox.moe's anonymous upload API: a multipart POST with
// reqtype=fileupload and fileToUpload; the response body is the file's URL.
const catboxEndpoint = "https://catbox.moe/user/api.php"

// uploadCatbox streams path to the endpoint, reporting progress (0..1) as
// bytes go out, and returns the link.
func uploadCatbox(ctx context.Context, endpoint, path string, onProgress func(float64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			if err := mw.WriteField("reqtype", "fileupload"); err != nil {
				return err
			}
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="fileToUpload"; filename="%s"`,
				strings.ReplaceAll(filepath.Base(path), `"`, "")))
			h.Set("Content-Type", "video/mp4")
			part, err := mw.CreatePart(h)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, &progressReader{r: f, total: fi.Size(), on: onProgress}); err != nil {
				return err
			}
			return mw.Close()
		}()
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", "Toru (github.com/StephenSHorton/toru)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(text, "https://") {
		if text == "" {
			text = resp.Status
		}
		return "", fmt.Errorf("upload failed: %s", text)
	}
	return text, nil
}

// progressReader reports how much of total has been read.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	on    func(float64)
	last  int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.on != nil && p.total > 0 {
		// Throttle to whole-percent steps so the UI isn't flooded with events.
		if pct := int(p.read * 100 / p.total); pct != p.last {
			p.last = pct
			p.on(float64(p.read) / float64(p.total))
		}
	}
	return n, err
}
