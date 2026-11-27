// fakewhisper stands in for whisper-server.exe in the engine's tests. The
// file "mode" next to it says how it behaves: "gpu" says it put the model
// on the GPU (as the CUDA build does) and serves, "nogpu" serves without
// saying so, "exit" quits at once. /inference answers with the mode, so a
// test can tell which build transcribed.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	port := flag.String("port", "", "")
	flag.String("host", "", "")
	flag.String("m", "", "")
	flag.String("t", "", "")
	flag.String("l", "", "")
	flag.String("nth", "", "")
	flag.String("public", "", "")
	flag.Bool("nt", false, "")
	flag.Bool("suppress-nst", false, "")
	flag.Parse()
	exe, _ := os.Executable()
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "mode"))
	mode := strings.TrimSpace(string(raw))
	switch mode {
	case "exit":
		os.Exit(1)
	case "gpu":
		fmt.Fprintln(os.Stderr, "whisper_backend_init_gpu: using CUDA0 backend")
	}
	http.HandleFunc("/inference", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"text":%q}`, mode)
	})
	_ = http.ListenAndServe("127.0.0.1:"+*port, nil)
}
