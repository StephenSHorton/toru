//go:build !windows

package webprint

import "context"

// Page is an HTML file open in a WebView2; there is none here.
type Page struct{}

// Open fails: there is no WebView2 off Windows.
func Open(context.Context, string, Options) (*Page, error) { return nil, ErrUnsupported }

func (p *Page) Eval(context.Context, string) error                 { return ErrUnsupported }
func (p *Page) Next(context.Context, string) (string, error)       { return "", ErrUnsupported }
func (p *Page) PrintPDF(context.Context, string, PDFOptions) error { return ErrUnsupported }
func (p *Page) Close()                                             {}
