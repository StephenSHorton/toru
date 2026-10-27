//go:build !windows

package stream

import "errors"

type noLanOS struct{}

func defaultLanOS() lanOS { return noLanOS{} }

func (noLanOS) supported() bool           { return false }
func (noLanOS) query() lanSvcInfo         { return lanSvcInfo{} }
func (noLanOS) start() error              { return errors.New("LAN needs Windows") }
func (noLanOS) runInstaller(string) error { return errors.New("LAN needs Windows") }
