// Copyright 2018 terf Authors. All rights reserved.
//
// This file is part of terf.
//
// terf is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// terf is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with terf.  If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"compress/zlib"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	log "github.com/sirupsen/logrus"
	"github.com/ubccr/terf"
	"golang.org/x/sync/errgroup"
)

const (
	InfoFile = "info.csv"
)

func Extract(inputPath, outPath string, threads int, compress bool) error {
	if len(outPath) == 0 {
		return errors.New("Please provide an output directory")
	}

	outdir, err := filepath.Abs(outPath)
	if err != nil {
		return err
	}

	err = os.MkdirAll(outdir, 0755)
	if err != nil {
		return err
	}

	if threads == 0 {
		threads = runtime.NumCPU()
	}

	stat, err := os.Stat(inputPath)
	if err != nil {
		return err
	}

	if !stat.IsDir() {
		images, err := extractFile(inputPath, outdir, compress)
		if err != nil {
			return err
		}

		if len(images) == 0 {
			return errors.New("No images found")
		}

		out, err := os.Create(filepath.Join(outdir, InfoFile))
		if err != nil {
			return err
		}
		defer out.Close()

		w := csv.NewWriter(out)
		err = writeHeader(w)
		if err != nil {
			return err
		}

		writeLabels(w, images)

		w.Flush()
		if err := w.Error(); err != nil {
			return err
		}

		return nil
	}

	g, ctx := errgroup.WithContext(context.TODO())
	paths := make(chan string)

	g.Go(func() error {
		defer close(paths)

		files, err := ioutil.ReadDir(inputPath)
		if err != nil {
			return err
		}

		for _, f := range files {
			if f.IsDir() {
				continue
			}

			select {
			case paths <- filepath.Join(inputPath, f.Name()):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})

	images := make(chan []*ImageRecord)

	for i := 0; i < threads; i++ {
		g.Go(func() error {
			for path := range paths {
				im, err := extractFile(path, outdir, compress)
				if err != nil {
					return err
				}

				select {
				case images <- im:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		})
	}

	go func() {
		g.Wait()
		close(images)
	}()

	out, err := os.Create(filepath.Join(outdir, InfoFile))
	if err != nil {
		return err
	}
	defer out.Close()

	w := csv.NewWriter(out)
	err = writeHeader(w)
	if err != nil {
		return err
	}

	for i := range images {
		writeLabels(w, i)
	}

	if err := g.Wait(); err != nil {
		return err
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}

	return nil
}

func writeHeader(w *csv.Writer) error {
	header := []string{
		"image_path",
		"id",
		"label_id",
		"label_text",
		"organization",
	}

	return w.Write(header)
}

func writeLabels(w *csv.Writer, images []*ImageRecord) error {
	for _, i := range images {
		record := []string{
			i.Path,
			strconv.Itoa(i.ID),
			strconv.Itoa(i.LabelID),
			i.LabelText,
			i.Organization,
		}

		if err := w.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func extractFile(inputPath, outdir string, compress bool) ([]*ImageRecord, error) {
	log.WithFields(log.Fields{
		"path": inputPath,
		"zlib": compress,
	}).Info("Processing file")

	in, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	var r *terf.Reader
	if compress {
		zin, err := zlib.NewReader(in)
		if err != nil {
			return nil, err
		}
		defer zin.Close()

		r = terf.NewReader(zin)
	} else {
		r = terf.NewReader(in)
	}

	images := make([]*ImageRecord, 0)

	for {
		ex, err := r.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		img, err := terf.NewImageFromExample(ex)
		if err != nil {
			return nil, err
		}

		if err := os.MkdirAll(filepath.Join(outdir, img.LabelText), 0755); err != nil {
			return nil, err
		}

		fname := filepath.Join(outdir, img.LabelText, fmt.Sprintf("%d.jpg", img.ID))

		err = img.Save(fname)
		if err != nil {
			return nil, err
		}

		ir := &ImageRecord{
			Path:         fname,
			ID:           img.ID,
			LabelID:      img.LabelID,
			LabelText:    img.LabelText,
			Organization: img.Organization,
		}

		images = append(images, ir)
	}

	return images, nil
}