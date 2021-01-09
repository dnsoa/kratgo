package invalidator

import (
	"io"

	logger "github.com/savsgio/go-logger/v2"
	"github.com/savsgio/kratgo/modules/cache"
	"github.com/savsgio/kratgo/modules/config"
)

// Config ...
type Config struct {
	FileConfig config.Invalidator
	Cache      *cache.Cache

	LogLevel  string
	LogOutput io.Writer
}

// Invalidator ...
type Invalidator struct {
	fileConfig config.Invalidator

	cache *cache.Cache

	activeWorkers int32

	chEntries chan Entry
	log       *logger.Logger
}

// EntryHeader ...
type EntryHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entry ...
type Entry struct {
	Host   string      `json:"host"`
	Path   string      `json:"path"`
	Header EntryHeader `json:"header"`
}

type invType int
