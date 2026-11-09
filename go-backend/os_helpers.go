package main

import "os"

func mkdirAll(path string) error                        { return os.MkdirAll(path, 0o755) }
func createTemp(path, pattern string) (*os.File, error) { return os.CreateTemp(path, pattern) }
func removeFile(path string) error                      { return os.Remove(path) }
