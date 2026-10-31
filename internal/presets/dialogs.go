package presets

import (
	"errors"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// dialogs.go holds the editor's file dialogs for presets and LUTs: the page
// can't open native dialogs itself, so each Pick* shows one and does the
// import or export. A cancelled dialog returns a zero value and no error.

var errNoApp = errors.New("no running application")

func openOne(title, filterName, pattern string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errNoApp
	}
	dlg := app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		AddFilter(filterName, pattern).
		AddFilter("All Files", "*.*")
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", nil // cancelled (the dialog reports cancel as an error)
	}
	return path, nil
}

// PickLut asks for a .cube file and imports it. ok is false when the dialog
// was cancelled.
func (s *Service) PickLut() (lut Lut, ok bool, err error) {
	path, err := openOne("Import LUT", "3D LUT (.cube)", "*.cube")
	if err != nil || path == "" {
		return Lut{}, false, err
	}
	l, err := s.ImportLut(path)
	return l, err == nil, err
}

// PickImport asks for a .torufx file and adds it to My filters. ok is false
// when the dialog was cancelled.
func (s *Service) PickImport() (preset Preset, ok bool, err error) {
	path, err := openOne("Import filter", "Toru filter (.torufx)", "*.torufx")
	if err != nil || path == "" {
		return Preset{}, false, err
	}
	p, err := s.Import(path)
	return p, err == nil, err
}

// PickExport asks where to save a filter as .torufx and writes it. It
// returns the path written, or "" when the dialog was cancelled.
func (s *Service) PickExport(id string) (string, error) {
	p, err := s.Get(id)
	if err != nil {
		return "", err
	}
	app := application.Get()
	if app == nil {
		return "", errNoApp
	}
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '-'
		}
		return r
	}, p.Name)
	dlg := app.Dialog.SaveFile().
		SetFilename(name+".torufx").
		AddFilter("Toru filter (.torufx)", "*.torufx")
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil
	}
	return s.Export(id, path)
}
