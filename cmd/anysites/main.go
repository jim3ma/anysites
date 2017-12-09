package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/jim3ma/anysites"
)

func main() {
	target, _ := url.Parse("https://www.google.com")
	director := anysites.NewDirector(target,
		"http",
		"127.0.0.1:8090",
		"/x/",
		[]string{
			"accounts.google.com",
			"admin.google.com",
			"adwords.google.com",
			"analytics.google.com",
			"apis.google.com",
			"books.google.com",
			"business.google.com",
			"calendar.google.com",
			"classroom.google.com",
			"cloud.google.com",
			"contacts.google.com",
			"developers.google.com",
			"docs.google.com",
			"drive.google.com",
			"encrypted.google.com",
			"google.com",
			"hangouts.google.com",
			"inbox.google.com",
			"ipv4.google.com",
			"keep.google.com",
			"mail.google.com",
			"myaccount.google.com",
			"news.google.com",
			"notifications.google.com",
			"photos.google.com",
			"play.google.com",
			"plus.google.com",
			"productforums.google.com",
			"scholar.google.com",
			"sites.google.com",
			"support.google.com",
			"translate.google.com",
			"url.google.com",

			"fonts.gstatic.com",
			"www.gstatic.com",

			"ajax.googleapis.com",
			"mannequin.storage.googleapis.com",
		})
	r := &httputil.ReverseProxy{
		Director:       director.Director,
		ModifyResponse: director.ModifyResponse,
	}

	http.Handle("/", r)
	http.ListenAndServe(":8090", nil)
}
