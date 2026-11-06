//go:build !linux && !darwin

package scanbench

import (
	"errors"
	"os"
	"time"
)

type fixtureDir struct{}

var errFixturePlatform = errors.New("secure fixture generation requires Linux or Darwin directory handles")

func createFixtureRoot(string) (*fixtureDir, error)    { return nil, errFixturePlatform }
func (*fixtureDir) Close() error                       { return nil }
func (*fixtureDir) bucket(string) (*fixtureDir, error) { return nil, errFixturePlatform }
func (*fixtureDir) create(string) (*os.File, error)    { return nil, errFixturePlatform }
func stampFixtureFile(*os.File, time.Time) error       { return errFixturePlatform }
