// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// On App Engine, the framework sets up main; we should be a different package.

// fork from AppEngine code, so use main
//package moustachio
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // import so we can read PNG files.
	"io"
	"os"
	"net/http"
	"strconv"
	"text/template"
	"code.google.com/p/goauth2/oauth"
	"crypto/sha1"
	//"appengine/urlfetch"
	"./resize"
)

const (
	// Created at http://code.google.com/apis/console, these identify
	// our app for the OAuth protocol.
	CLIENT_ID     = "Your Client ID here."
	CLIENT_SECRET = "Your Client Secret here."
	localDir = "/home/wensheng/others/appengine-go/example/mymoustachio/static"
)

// config returns the configuration information for OAuth and Buzz.
func config(host string) *oauth.Config {
	return &oauth.Config{
		ClientId:     CLIENT_ID,
		ClientSecret: CLIENT_SECRET,
		Scope:        "https://www.googleapis.com/auth/buzz",
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
		RedirectURL:  fmt.Sprintf("http://%s/post", host),
	}
}

var (
	templates = template.Must(template.ParseFiles(
		"templates/edit.html",
		"templates/error.html",
		"templates/post.html",
		"templates/upload.html",
	))
)

// Image is the type used to hold the image in the datastore.
type Image struct {
	Data []byte
}

// upload is the HTTP handler for uploading images; it handles "/".
func upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		// No upload; show the upload form.
		templates.ExecuteTemplate(w, "upload.html", nil)
		return
	}

	f, _, err := r.FormFile("image")
	check(err)
	defer f.Close()

	// Grab the image data
	var buf bytes.Buffer
	io.Copy(&buf, f)
	i, _, err := image.Decode(&buf)
	check(err)

	// Resize if too large, for more efficient moustachioing.
	// We aim for less than 1200 pixels in any dimension; if the
	// picture is larger than that, we squeeze it down to 600.
	const max = 1200
	if b := i.Bounds(); b.Dx() > max || b.Dy() > max {
		// If it's gigantic, it's more efficient to downsample first
		// and then resize; resizing will smooth out the roughness.
		if b.Dx() > 2*max || b.Dy() > 2*max {
			w, h := max, max
			if b.Dx() > b.Dy() {
				h = b.Dy() * h / b.Dx()
			} else {
				w = b.Dx() * w / b.Dy()
			}
			i = resize.Resample(i, i.Bounds(), w, h)
			b = i.Bounds()
		}
		w, h := max/2, max/2
		if b.Dx() > b.Dy() {
			h = b.Dy() * h / b.Dx()
		} else {
			w = b.Dx() * w / b.Dy()
		}
		i = resize.Resize(i, i.Bounds(), w, h)
	}

	// Encode as a new JPEG image.
	buf.Reset()
	err = jpeg.Encode(&buf, i, nil)
	check(err)

	// Save the image under a unique key, a hash of the image.
	key := keyOf(buf.Bytes())
	fo, err := os.Create("static/uploads/"+key,)
	defer fo.Close()

	fo.Write(buf.Bytes());

	http.Redirect(w, r, "/edit?id="+key, http.StatusFound)
}

// keyOf returns (part of) the SHA-1 hash of the data, as a hex string.
func keyOf(data []byte) string {
	sha := sha1.New()
	sha.Write(data)
	return fmt.Sprintf("%x", string(sha.Sum(nil))[0:8])
}

// edit is the HTTP handler for editing images; it handles "/edit".
func edit(w http.ResponseWriter, r *http.Request) {
	templates.ExecuteTemplate(w, "edit.html", r.FormValue("id"))
}

// img is the HTTP handler for displaying images and painting moustaches;
// it handles "/img".
func img(w http.ResponseWriter, r *http.Request) {
	imageFile, err := os.Open("static/uploads/"+r.FormValue("id"))
	check(err)

	m, _, err := image.Decode(imageFile)
	check(err)

	get := func(n string) int { // helper closure
		i, _ := strconv.Atoi(r.FormValue(n))
		return i
	}
	x, y, s, d := get("x"), get("y"), get("s"), get("d")

	if x > 0 { // only draw if coordinates provided
		m = moustache(m, x, y, s, d)
	}

	w.Header().Set("Content-type", "image/jpeg")
	jpeg.Encode(w, m, nil)
}

// share is the HTTP handler that redirects the user to authenticate
// with OAuth; it handles "/share".
func share(w http.ResponseWriter, r *http.Request) {
	url := config(r.Host).AuthCodeURL(r.URL.RawQuery)
	http.Redirect(w, r, url, http.StatusFound)
}

// post is the HTTP handler that receives the redirection from OAuth
// and posts the image to the Buzz stream; it handles "/share".
func post(w http.ResponseWriter, r *http.Request) {
	// Exchange code for an access token at OAuth provider.
	code := r.FormValue("code")
	t := &oauth.Transport{
		Config: config(r.Host),
		Transport: &urlfetch.Transport{
			Context: appengine.NewContext(r),
		},
	}
	_, err := t.Exchange(code)
	check(err)

	// Post the image to the user's activity stream.
	image := r.FormValue("state")
	url := fmt.Sprintf("http://%s/img?%s", r.Host, image)
	err = postPhoto(t.Client(), url)
	check(err)

	templates.ExecuteTemplate(w, "post.html", url)
}

// postPhoto uses the Buzz API to post the image to the user's Buzz stream.
func postPhoto(client *http.Client, photoURL string) error {
	const url = "https://www.googleapis.com/buzz/v1/activities/@me/@self"
	const text = "Moustachio"

	type m map[string]interface{}
	post := m{"data": m{"object": m{
		"type":    "note",
		"content": text,
		"attachments": []m{{
			"type":    "photo",
			"content": text,
			"links": m{
				"enclosure": []m{{
					"href": photoURL,
					"type": "image/jpeg",
				}},
			},
		}},
	}}}

	b, err := json.Marshal(post)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return errors.New("invalid post " + resp.Status)
	}
	return nil
}

// errorHandler wraps the argument handler with an error-catcher that
// returns a 500 HTTP error if the request fails (calls check with err non-nil).
func errorHandler(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err, ok := recover().(error); ok {
				w.WriteHeader(http.StatusInternalServerError)
				templates.ExecuteTemplate(w, "error.html", err)
			}
		}()
		fn(w, r)
	}
}

// check aborts the current execution if err is non-nil.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	http.HandleFunc("/", errorHandler(upload))
	http.Handle("/static/", http.StripPrefix("/static",http.FileServer(http.Dir("./static"))))
	http.HandleFunc("/edit", errorHandler(edit))
	http.HandleFunc("/img", errorHandler(img))
	http.HandleFunc("/share", errorHandler(share))
	http.HandleFunc("/post", errorHandler(post))
	http.ListenAndServe(":8080", nil)
}
