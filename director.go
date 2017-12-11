package anysites

import (
	"bytes"
	"compress/gzip"
	"compress/flate"
	"errors"
	"io/ioutil"
	"log"
	"net/url"
	"net/http"
	//"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

type Director struct {
	target       *url.URL
	schema       string
	domain       string
	subdomains   []string
	domainPrefix string
}

func NewDirector(target *url.URL, schema, domain, domainPrefifx string, subdomains []string) *Director {
	return &Director{
		target:       target,
		schema:       schema,
		domain:       domain,
		domainPrefix: domainPrefifx,
		subdomains:   subdomains,
	}
}

func (d *Director) Director(req *http.Request) {
	log.Printf("request url: %#v", req.URL)
	log.Printf("request header: %#v", req.Header)

	// TODO some request with header "Range", the response with return with 206, we need cached first to replace url

	if hasSubdomain(req, d.domainPrefix) {
		d.updateSubRequest(req)
	} else {
		d.updateTargetRequest(req)
	}

	// https://golang.org/src/net/http/request.go?h=RequestURI#L267
	req.RequestURI = ""

	// remove unsupported content encoding
	CE := strings.Split(req.Header.Get("Accept-Encoding"), ",")
	newCE := make([]string, 0)
	for _, v := range CE {
		switch strings.TrimSpace(v) {
		case "gzip", "deflate":
			newCE = append(newCE, v)
		}
	}
	req.Header.Set("Accept-Encoding", strings.Join(newCE, ", "))
	//req.Header.Del("Accept-Encoding")
}

func hasSubdomain(req *http.Request, prefix string) bool {
	return strings.HasPrefix(req.URL.Path, prefix)
}

func (d *Director) updateSubRequest(req *http.Request) error {
	// http://127.0.0.1/{domainPrefix}/{schema}/{domain}/
	u := strings.Split(req.URL.Path, "/")
	log.Printf("url: %#v", u)
	if len(u) < 4 {
		return errors.New("too short path")
	}
	req.URL.Path = "/" + strings.Join(u[4:], "/")
	req.URL.Host = u[3]
	req.URL.Scheme = u[2]
	req.Host = u[3]
	log.Printf("update: %#v", req.URL)
	return nil
}

func (d *Director) updateTargetRequest(req *http.Request) {
	req.URL.Scheme = d.target.Scheme
	req.URL.Host = d.target.Host
	req.Host = d.target.Host
	req.URL.Path = singleJoiningSlash(d.target.Path, req.URL.Path)

	if d.target.RawQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = d.target.RawQuery + req.URL.RawQuery
	} else {
		req.URL.RawQuery = d.target.RawQuery + "&" + req.URL.RawQuery
	}
}

func (d *Director) ModifyResponse(resp *http.Response) error {
	// update Location for Status 30x
	if resp.StatusCode > 299 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		newLoc := d.decorateUrl(resp.Request, loc)
		log.Printf("location: %#v, new: %#v", loc, newLoc)
		resp.Header.Set("Location", newLoc)
	}

	ct, ok := resp.Header["Content-Type"]
	if !ok {
		ct, ok = resp.Header["content-type"]
		if !ok {
			return nil
		}
	}
	// log.Printf("response header: %#v", resp.Header)

	// TODO handle Content-Type before pass to ReverseProxy, non "text/html" resource should be bypass, cache and so on.
	if strings.Contains(strings.Join(ct, "; "), "text/html") {
		var bodyBytes []byte

		//log.Printf("Origin Response: %#v", resp)
		log.Printf("Response Header: %s", resp.Header)
		log.Printf("Content Length: %#v", resp.ContentLength)
		log.Printf("Status:%#v, Code: %#v", resp.Status, resp.StatusCode)
		if resp.StatusCode == http.StatusPartialContent {
			return nil
		}
		switch resp.Header.Get("Content-Encoding") {
		// currently, the pure go brotli library didn't support NewWriter method
		/*
		case "br":
			r, _ := brotli.NewReader(resp.Body, nil)
			bodyBytes, _ = ioutil.ReadAll(r)
			resp.Body.Close()
		*/
		case "gzip":
			r, err := gzip.NewReader(resp.Body)
			if err != nil {
				return err
			}
			bodyBytes, _ = ioutil.ReadAll(r)
			resp.Body.Close()
		case "deflate":
			r := flate.NewReader(resp.Body)
			bodyBytes, _ = ioutil.ReadAll(r)
			resp.Body.Close()
		default:
			bodyBytes, _ = ioutil.ReadAll(resp.Body)
			resp.Body.Close()
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer(bodyBytes))
		if err != nil {
			return err
		}

		//log.Printf("response body: %s", string(bodyBytes))
		/*
		replace domain in <a href=""></a>, <link rel="stylesheet" href="">
		 */
		for _, tag := range []string{"a", "link"} {
			doc.Find(tag).Each(d.replaceDomain(resp.Request, "href"))
		}

		/*
		replace domain in <script src=""/>, <style src=""/>, <img src="/>
		 */
		for _, tag := range []string{"img", "script", "style"} {
			doc.Find(tag).Each(d.replaceDomain(resp.Request, "src"))
		}

		// TODO replace url in javascript, css style

		str, err := doc.Html()
		if err != nil {
			log.Printf("error: %s", err)
			return nil
		}
		var buffer bytes.Buffer

		n := -1
		switch resp.Header.Get("Content-Encoding") {
		// currently, the pure go brotli library didn't support NewWriter method
		/*
		case "br":
			r, _ := brotli.NewReader(resp.Body, nil)
			bodyBytes, _ = ioutil.ReadAll(r)
			resp.Body.Close()
		*/
		case "gzip":
			w := gzip.NewWriter(&buffer)
			n, err = w.Write([]byte(str))
			if err != nil {
				return err
			}
			w.Close()
			resp.Body = ioutil.NopCloser(&buffer)
		case "deflate":
			w, _ := flate.NewWriter(&buffer, 1)
			n, err = w.Write([]byte(str))
			if err != nil {
				return err
			}
			w.Close()
			resp.Body = ioutil.NopCloser(&buffer)
		default:
			resp.Body = ioutil.NopCloser(strings.NewReader(str))
			n = len([]byte(str))
			delete(resp.Header, "Content-Encoding")
		}
		// see https://github.com/mholt/caddy/issues/38#issuecomment-98359909
		resp.Header.Del("Content-Length")
		if n != -1 {
			resp.ContentLength = int64(n)
		}

		//log.Printf("Content Length after replace: %#v", resp.ContentLength)
		//log.Printf("Rebuild Response: %#v", resp)
	}
	return nil
}

// TODO update header for 302 301
func (d *Director) updateHeader(req *http.Request, resp *http.Response) {

}

func (d *Director) decorateUrl(req *http.Request, old string) (newUrl string) {
	// prefix with slash "/", "../"
	if strings.HasPrefix(old, "/") && ! strings.HasPrefix(old, "//") {
		log.Printf("slash start url: %#v", old)
		// update target domain
		if req.Host == d.target.Host {
			newUrl = d.schema + "://" + d.domain + old
			return
		}

		// update subdomain
		for _, domain := range d.subdomains {
			if domain == req.Host {
				// http://127.0.0.1/{domainPrefix}/{schema}/{domain}/
				newUrl = d.schema + "://" + d.domain + d.domainPrefix + req.URL.Scheme + "/" + domain + old
				return
			}
		}
		newUrl = old
		return
	} else if strings.HasPrefix(old, "../") {
		// TODO
	}

	// target host
	for _, post := range []string{"/", "?"} {
		for _, schema := range []string{"http://", "https://", "//"} {
			if strings.HasPrefix(old, schema+d.target.Host+post) {
				if d.schema == schema {
					newUrl = strings.Replace(old, d.target.Host, d.domain, 1)
				} else {
					newUrl = strings.Replace(old, schema+d.target.Host,
						d.schema+"://"+d.domain+d.domainPrefix+req.URL.Scheme+"/"+d.target.Host, 1)
				}
				return
			}
		}
	}

	// sub domain
	for _, domain := range d.subdomains {
		// schema: https:// http:// //
		// post: / ?
		for _, post := range []string{"/", "?"} {
			for _, schema := range []string{"http://", "https://", "//"} {
				if strings.HasPrefix(old, schema+domain+post) {
					newUrl = strings.Replace(old, schema+domain+post,
						d.schema+"://"+d.domain+d.domainPrefix+req.URL.Scheme+"/"+domain+post, 1)
					return
				}
			}
		}
	}
	newUrl = old
	return
}

func (d *Director) replaceDomain(req *http.Request, attrName string) func(i int, s *goquery.Selection) {
	return func(i int, s *goquery.Selection) {
		attr, exist := s.Attr(attrName)
		if !exist {
			return
		}

		newAttr := d.decorateUrl(req, attr)
		log.Printf("replace domain in %s: %#v with new: %s", attrName, attr, newAttr)

		s.SetAttr(attrName, newAttr)
	}
}
