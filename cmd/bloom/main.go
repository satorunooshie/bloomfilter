// Command bloom provides small file-based operations for portable Bloom
// filters. It is intentionally byte/line oriented and uses the deterministic
// bloom/portable implementation.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/satorunooshie/bloomfilter/portable"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "create":
		create(os.Args[2:])
	case "insert":
		insert(os.Args[2:])
	case "check":
		check(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func create(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	n := fs.Uint64("capacity", 100_000, "expected number of values")
	p := fs.Float64("rate", 0.01, "target false-positive rate")
	file := fs.String("file", "filter.bloom", "output file")
	fs.Parse(args)
	f, err := portable.New(*n, *p)
	fatal(err)
	data, err := f.MarshalBinary()
	fatal(err)
	saveBytes(*file, data)
}

func insert(args []string) {
	fs := flag.NewFlagSet("insert", flag.ExitOnError)
	file := fs.String("file", "filter.bloom", "filter file")
	fs.Parse(args)
	f := load(*file)
	forEachLine(os.Stdin, func(line []byte) { f.Add(line) })
	save(*file, f)
}

func check(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	file := fs.String("file", "filter.bloom", "filter file")
	fs.Parse(args)
	f := load(*file)
	out := bufio.NewWriter(os.Stdout)
	forEachLine(os.Stdin, func(line []byte) {
		if f.Contains(line) {
			if _, err := out.Write(line); err != nil {
				fatal(err)
			}
			fatal(out.WriteByte('\n'))
		}
	})
	fatal(out.Flush())
}

func load(file string) *portable.Filter {
	data, err := os.ReadFile(file)
	fatal(err)
	f := new(portable.Filter)
	fatal(f.UnmarshalBinary(data))
	return f
}

func save(file string, f *portable.Filter) {
	data, err := f.MarshalBinary()
	fatal(err)
	saveBytes(file, data)
}

func saveBytes(file string, data []byte) {
	dir := "."
	if i := indexLastSlash(file); i >= 0 {
		dir = file[:i]
		if dir == "" {
			dir = "/"
		}
	}
	tmp, err := os.CreateTemp(dir, ".bloom-*")
	fatal(err)
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	fatal(tmp.Chmod(0o600))
	n, err := tmp.Write(data)
	if err != nil {
		fatal(err)
	}
	if n != len(data) {
		fatal(io.ErrShortWrite)
	}
	fatal(tmp.Sync())
	fatal(tmp.Close())
	fatal(os.Rename(tmpName, file))
}

func forEachLine(r io.Reader, fn func([]byte)) {
	buf := make([]byte, 64*1024)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for {
				i := indexNewline(pending)
				if i < 0 {
					break
				}
				line := pending[:i]
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				fn(line)
				pending = pending[i+1:]
			}
		}
		if err == io.EOF {
			if len(pending) > 0 {
				fn(pending)
			}
			return
		}
		fatal(err)
	}
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

func indexLastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func usage() { fmt.Fprintln(os.Stderr, "usage: bloom {create|insert|check} [flags]") }
