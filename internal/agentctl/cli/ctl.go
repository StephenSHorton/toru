package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunCtl runs `toru.exe ctl <command> [--json '{...}']`: one call, the
// result printed as indented JSON on stdout. It returns the exit code.
func RunCtl(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return usage(env.Stderr)
	}
	name := args[0]
	input := json.RawMessage(`{}`)
	for i := 1; i < len(args); i++ {
		a := args[i]
		val, inline := strings.CutPrefix(a, "--json=")
		switch {
		case inline:
		case a == "--json" && i+1 < len(args):
			i++
			val = args[i]
		default:
			sayf(env.Stderr, "toru ctl: unexpected argument %q\n", a)
			return usage(env.Stderr)
		}
		if val == "-" {
			b, err := io.ReadAll(env.Stdin)
			if err != nil {
				sayln(env.Stderr, "toru ctl: read stdin:", err)
				return exitUsage
			}
			val = string(b)
		}
		val = strings.TrimSpace(val)
		if !json.Valid([]byte(val)) || !strings.HasPrefix(val, "{") {
			sayln(env.Stderr, "toru ctl: --json must be a JSON object, e.g. --json '{\"t\":12.5}'")
			return exitUsage
		}
		input = json.RawMessage(val)
	}

	c := NewClient(env.Dial)
	defer func() { _ = c.Close() }()

	var out any
	var err error
	switch name {
	case "list":
		var cmds []CommandInfo
		cmds, err = c.List(ctx)
		if err == nil {
			out = append([]CommandInfo{appStartInfo}, cmds...)
		}
	case appStartName:
		out, err = appStart(ctx, c, env)
	default:
		var raw json.RawMessage
		raw, err = c.Call(ctx, name, input)
		if err == nil {
			out, err = ctlResult(raw)
		}
	}

	if err != nil {
		var cmdErr *CommandError
		switch {
		case errors.As(err, &cmdErr):
			sayln(env.Stderr, "toru ctl:", cmdErr.Message)
			return exitCommand
		case errors.Is(err, ErrNotToru):
			sayln(env.Stderr, "toru ctl:", err)
			return exitUnreachable
		case errors.Is(err, errUnreachable):
			sayln(env.Stderr, "toru ctl:", unreachableMessage(env, "run `toru.exe ctl app.start`"))
			return exitUnreachable
		}
		sayln(env.Stderr, "toru ctl:", err)
		return exitCommand
	}
	enc := json.NewEncoder(env.Stdout)
	enc.SetEscapeHTML(false) // keep "Settings > General" readable
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return exitOK
}

// ctlResult is a call's result as ctl prints it: inline images are saved to
// a temp file and replaced by its path, so a terminal never gets megabytes
// of base64.
func ctlResult(raw json.RawMessage) (any, error) {
	img, ok := parseImage(raw)
	if !ok {
		return raw, nil
	}
	ext := ".png"
	if strings.Contains(img.MIME, "jpeg") {
		ext = ".jpg"
	}
	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, fmt.Sprintf("ctl-%d%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(p, img.Data, 0o644); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, v := range img.Rest {
		out[k] = v
	}
	out["image"] = map[string]string{"mime": img.MIME, "path": p}
	return out, nil
}

type imageResult struct {
	MIME string
	Data []byte
	Rest map[string]json.RawMessage // the result's other fields
}

// parseImage recognises a result carrying {"image":{"mime","base64"}}.
func parseImage(raw json.RawMessage) (imageResult, bool) {
	if !bytes.Contains(raw, []byte(`"image"`)) {
		return imageResult{}, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return imageResult{}, false
	}
	var img struct {
		MIME   string `json:"mime"`
		Base64 string `json:"base64"`
	}
	if json.Unmarshal(obj["image"], &img) != nil || img.Base64 == "" {
		return imageResult{}, false
	}
	data, err := base64.StdEncoding.DecodeString(img.Base64)
	if err != nil {
		return imageResult{}, false
	}
	if img.MIME == "" {
		img.MIME = "image/png"
	}
	delete(obj, "image")
	return imageResult{MIME: img.MIME, Data: data, Rest: obj}, true
}
