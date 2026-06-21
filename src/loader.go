package main

import "os"

func openFile(path string) (*TrackData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return ParseTrackData(string(data))
}
