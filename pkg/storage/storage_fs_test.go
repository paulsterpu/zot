package storage_test

import (
	"bytes"
	_ "crypto/sha256"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"

	"github.com/anuvu/zot/errors"
	"github.com/anuvu/zot/pkg/log"
	"github.com/anuvu/zot/pkg/storage"
	godigest "github.com/opencontainers/go-digest"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/zerolog"
	. "github.com/smartystreets/goconvey/convey"
)

func TestDedupeLinks(t *testing.T) {
	dir, err := ioutil.TempDir("", "oci-repo-test")
	if err != nil {
		panic(err)
	}

	defer os.RemoveAll(dir)

	il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})

	Convey("Dedupe", t, func(c C) {
		blobDigest1 := ""
		blobDigest2 := ""

		// manifest1
		v, err := il.NewBlobUpload("dedupe1")
		So(err, ShouldBeNil)
		So(v, ShouldNotBeEmpty)

		content := []byte("test-data3")
		buf := bytes.NewBuffer(content)
		l := buf.Len()
		d := godigest.FromBytes(content)
		b, err := il.PutBlobChunkStreamed("dedupe1", v, buf)
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)
		blobDigest1 = strings.Split(d.String(), ":")[1]
		So(blobDigest1, ShouldNotBeEmpty)

		err = il.FinishBlobUpload("dedupe1", v, buf, d.String())
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)

		_, _, err = il.CheckBlob("dedupe1", d.String())
		So(err, ShouldBeNil)

		_, _, err = il.GetBlob("dedupe1", d.String(), "application/vnd.oci.image.layer.v1.tar+gzip")
		So(err, ShouldBeNil)

		m := ispec.Manifest{}
		m.SchemaVersion = 2
		m = ispec.Manifest{
			Config: ispec.Descriptor{
				Digest: d,
				Size:   int64(l),
			},
			Layers: []ispec.Descriptor{
				{
					MediaType: "application/vnd.oci.image.layer.v1.tar",
					Digest:    d,
					Size:      int64(l),
				},
			},
		}
		m.SchemaVersion = 2
		mb, _ := json.Marshal(m)
		d = godigest.FromBytes(mb)
		_, err = il.PutImageManifest("dedupe1", d.String(), ispec.MediaTypeImageManifest, mb)
		So(err, ShouldBeNil)

		_, _, _, err = il.GetImageManifest("dedupe1", d.String())
		So(err, ShouldBeNil)

		// manifest2
		v, err = il.NewBlobUpload("dedupe2")
		So(err, ShouldBeNil)
		So(v, ShouldNotBeEmpty)

		content = []byte("test-data3")
		buf = bytes.NewBuffer(content)
		l = buf.Len()
		d = godigest.FromBytes(content)
		b, err = il.PutBlobChunkStreamed("dedupe2", v, buf)
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)
		blobDigest2 = strings.Split(d.String(), ":")[1]
		So(blobDigest2, ShouldNotBeEmpty)

		err = il.FinishBlobUpload("dedupe2", v, buf, d.String())
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)

		_, _, err = il.CheckBlob("dedupe2", d.String())
		So(err, ShouldBeNil)

		_, _, err = il.GetBlob("dedupe2", d.String(), "application/vnd.oci.image.layer.v1.tar+gzip")
		So(err, ShouldBeNil)

		m = ispec.Manifest{}
		m.SchemaVersion = 2
		m = ispec.Manifest{
			Config: ispec.Descriptor{
				Digest: d,
				Size:   int64(l),
			},
			Layers: []ispec.Descriptor{
				{
					MediaType: "application/vnd.oci.image.layer.v1.tar",
					Digest:    d,
					Size:      int64(l),
				},
			},
		}
		m.SchemaVersion = 2
		mb, _ = json.Marshal(m)
		d = godigest.FromBytes(mb)
		_, err = il.PutImageManifest("dedupe2", "1.0", ispec.MediaTypeImageManifest, mb)
		So(err, ShouldBeNil)

		_, _, _, err = il.GetImageManifest("dedupe2", d.String())
		So(err, ShouldBeNil)

		// verify that dedupe with hard links happened
		fi1, err := os.Stat(path.Join(dir, "dedupe2", "blobs", "sha256", blobDigest1))
		So(err, ShouldBeNil)
		fi2, err := os.Stat(path.Join(dir, "dedupe2", "blobs", "sha256", blobDigest2))
		So(err, ShouldBeNil)
		So(os.SameFile(fi1, fi2), ShouldBeTrue)
	})
}

func TestDedupe(t *testing.T) {
	Convey("Dedupe", t, func(c C) {
		Convey("Nil ImageStore", func() {
			var is storage.ImageStore
			So(func() { _ = is.DedupeBlob("", "", "") }, ShouldPanic)
		})

		Convey("Valid ImageStore", func() {
			dir, err := ioutil.TempDir("", "oci-repo-test")
			if err != nil {
				panic(err)
			}
			defer os.RemoveAll(dir)

			is := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})

			So(is.DedupeBlob("", "", ""), ShouldNotBeNil)
		})
	})
}

func TestNegativeCases(t *testing.T) {
	Convey("Invalid root dir", t, func(c C) {
		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		os.RemoveAll(dir)

		So(storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)}), ShouldNotBeNil)
		if os.Geteuid() != 0 {
			So(storage.NewImageStoreFS("/deadBEEF", true, true, log.Logger{Logger: zerolog.New(os.Stdout)}), ShouldBeNil)
		}
	})

	Convey("Invalid init repo", t, func(c C) {
		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})
		err = os.Chmod(dir, 0000) // remove all perms
		So(err, ShouldBeNil)
		if os.Geteuid() != 0 {
			err = il.InitRepo("test")
			So(err, ShouldNotBeNil)
		}

		err = os.Chmod(dir, 0755)
		So(err, ShouldBeNil)

		// Init repo should fail if repo is a file.
		err = ioutil.WriteFile(path.Join(dir, "file-test"), []byte("this is test file"), 0755) // nolint:gosec
		So(err, ShouldBeNil)
		err = il.InitRepo("file-test")
		So(err, ShouldNotBeNil)

		err = os.Mkdir(path.Join(dir, "test-dir"), 0755)
		So(err, ShouldBeNil)

		err = il.InitRepo("test-dir")
		So(err, ShouldBeNil)
	})

	Convey("Invalid validate repo", t, func(c C) {
		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})
		So(il, ShouldNotBeNil)
		So(il.InitRepo("test"), ShouldBeNil)

		err = os.MkdirAll(path.Join(dir, "invalid-test"), 0755)
		So(err, ShouldBeNil)

		err = os.Chmod(path.Join(dir, "invalid-test"), 0000) // remove all perms
		So(err, ShouldBeNil)

		_, err = il.ValidateRepo("invalid-test")
		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, errors.ErrRepoNotFound)

		err = os.Chmod(path.Join(dir, "invalid-test"), 0755) // remove all perms
		So(err, ShouldBeNil)

		err = ioutil.WriteFile(path.Join(dir, "invalid-test", "blobs"), []byte{}, 0755) // nolint: gosec
		So(err, ShouldBeNil)

		err = ioutil.WriteFile(path.Join(dir, "invalid-test", "index.json"), []byte{}, 0755) // nolint: gosec
		So(err, ShouldBeNil)

		err = ioutil.WriteFile(path.Join(dir, "invalid-test", ispec.ImageLayoutFile), []byte{}, 0755) // nolint: gosec
		So(err, ShouldBeNil)

		isValid, err := il.ValidateRepo("invalid-test")
		So(err, ShouldBeNil)
		So(isValid, ShouldEqual, false)

		err = os.Remove(path.Join(dir, "invalid-test", "blobs"))
		So(err, ShouldBeNil)

		err = os.Mkdir(path.Join(dir, "invalid-test", "blobs"), 0755)
		So(err, ShouldBeNil)

		isValid, err = il.ValidateRepo("invalid-test")
		So(err, ShouldNotBeNil)
		So(isValid, ShouldEqual, false)

		err = ioutil.WriteFile(path.Join(dir, "invalid-test", ispec.ImageLayoutFile), []byte("{}"), 0755) // nolint: gosec
		So(err, ShouldBeNil)

		isValid, err = il.ValidateRepo("invalid-test")
		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, errors.ErrRepoBadVersion)
		So(isValid, ShouldEqual, false)

		files, err := ioutil.ReadDir(path.Join(dir, "test"))
		So(err, ShouldBeNil)
		for _, f := range files {
			os.Remove(path.Join(dir, "test", f.Name()))
		}
		_, err = il.ValidateRepo("test")
		So(err, ShouldNotBeNil)
		os.RemoveAll(path.Join(dir, "test"))
		_, err = il.ValidateRepo("test")
		So(err, ShouldNotBeNil)
		err = os.Chmod(dir, 0000) // remove all perms
		So(err, ShouldBeNil)
		if os.Geteuid() != 0 {
			So(func() { _, _ = il.ValidateRepo("test") }, ShouldPanic)
		}
		os.RemoveAll(dir)
		_, err = il.GetRepositories()
		So(err, ShouldNotBeNil)
	})

	Convey("Invalid get image tags", t, func(c C) {
		var ilfs storage.ImageStoreFS
		_, err := ilfs.GetImageTags("test")
		So(err, ShouldNotBeNil)

		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})
		So(il, ShouldNotBeNil)
		So(il.InitRepo("test"), ShouldBeNil)
		So(os.Remove(path.Join(dir, "test", "index.json")), ShouldBeNil)
		_, err = il.GetImageTags("test")
		So(err, ShouldNotBeNil)
		So(os.RemoveAll(path.Join(dir, "test")), ShouldBeNil)
		So(il.InitRepo("test"), ShouldBeNil)
		So(ioutil.WriteFile(path.Join(dir, "test", "index.json"), []byte{}, 0600), ShouldBeNil)
		_, err = il.GetImageTags("test")
		So(err, ShouldNotBeNil)
	})

	Convey("Invalid get image manifest", t, func(c C) {
		var ilfs storage.ImageStoreFS
		_, _, _, err := ilfs.GetImageManifest("test", "")
		So(err, ShouldNotBeNil)

		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})
		So(il, ShouldNotBeNil)
		So(il.InitRepo("test"), ShouldBeNil)
		So(os.Remove(path.Join(dir, "test", "index.json")), ShouldBeNil)
		_, _, _, err = il.GetImageManifest("test", "")
		So(err, ShouldNotBeNil)
		So(os.RemoveAll(path.Join(dir, "test")), ShouldBeNil)
		So(il.InitRepo("test"), ShouldBeNil)
		So(ioutil.WriteFile(path.Join(dir, "test", "index.json"), []byte{}, 0600), ShouldBeNil)
		_, _, _, err = il.GetImageManifest("test", "")
		So(err, ShouldNotBeNil)
	})

	Convey("Invalid dedupe sceanrios", t, func() {
		dir, err := ioutil.TempDir("", "oci-repo-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)

		il := storage.NewImageStoreFS(dir, true, true, log.Logger{Logger: zerolog.New(os.Stdout)})
		v, err := il.NewBlobUpload("dedupe1")
		So(err, ShouldBeNil)
		So(v, ShouldNotBeEmpty)

		content := []byte("test-data3")
		buf := bytes.NewBuffer(content)
		l := buf.Len()
		d := godigest.FromBytes(content)
		b, err := il.PutBlobChunkStreamed("dedupe1", v, buf)
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)

		blobDigest1 := strings.Split(d.String(), ":")[1]
		So(blobDigest1, ShouldNotBeEmpty)

		err = il.FinishBlobUpload("dedupe1", v, buf, d.String())
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)

		// Create a file at the same place where FinishBlobUpload will create
		err = il.InitRepo("dedupe2")
		So(err, ShouldBeNil)

		err = os.MkdirAll(path.Join(dir, "dedupe2", "blobs/sha256"), 0755)
		So(err, ShouldBeNil)

		err = ioutil.WriteFile(path.Join(dir, "dedupe2", "blobs/sha256", blobDigest1), content, 0755) // nolint: gosec
		So(err, ShouldBeNil)

		v, err = il.NewBlobUpload("dedupe2")
		So(err, ShouldBeNil)
		So(v, ShouldNotBeEmpty)

		content = []byte("test-data3")
		buf = bytes.NewBuffer(content)
		l = buf.Len()
		d = godigest.FromBytes(content)
		b, err = il.PutBlobChunkStreamed("dedupe2", v, buf)
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)

		cmd := exec.Command("sudo", "chattr", "+i", path.Join(dir, "dedupe2", "blobs/sha256", blobDigest1)) // nolint: gosec
		_, err = cmd.Output()
		if err != nil {
			panic(err)
		}

		err = il.FinishBlobUpload("dedupe2", v, buf, d.String())
		So(err, ShouldNotBeNil)
		So(b, ShouldEqual, l)

		cmd = exec.Command("sudo", "chattr", "-i", path.Join(dir, "dedupe2", "blobs/sha256", blobDigest1)) // nolint: gosec
		_, err = cmd.Output()
		if err != nil {
			panic(err)
		}

		err = il.FinishBlobUpload("dedupe2", v, buf, d.String())
		So(err, ShouldBeNil)
		So(b, ShouldEqual, l)
	})
}

func TestHardLink(t *testing.T) {
	Convey("Test if filesystem supports hardlink", t, func() {
		dir, err := ioutil.TempDir("", "storage-hard-test")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)

		err = storage.ValidateHardLink(dir)
		So(err, ShouldBeNil)

		err = ioutil.WriteFile(path.Join(dir, "hardtest.txt"), []byte("testing hard link code"), 0644) //nolint: gosec
		if err != nil {
			panic(err)
		}

		err = os.Chmod(dir, 0400)
		if err != nil {
			panic(err)
		}

		err = storage.CheckHardLink(path.Join(dir, "hardtest.txt"), path.Join(dir, "duphardtest.txt"))
		So(err, ShouldNotBeNil)

		err = os.Chmod(dir, 0644)
		if err != nil {
			panic(err)
		}
	})
}