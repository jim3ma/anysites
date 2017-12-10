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
	"strconv"
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
		// log.Printf("CE: %s", resp.Header.Get("Content-Encoding"))
		switch resp.Header.Get("Content-Encoding") {
		// currently, the pure go brotli library didn't support NewWriter method
		/*
		case "br":
			r, _ := brotli.NewReader(resp.Body, nil)
			bodyBytes, _ = ioutil.ReadAll(r)
			resp.Body.Close()
		*/
		case "gzip":
			r, _ := gzip.NewReader(resp.Body)
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

		/*
		replace domain in <a href=""></a>, <link rel="stylesheet" href="">
		 */
		doc.Find("a").Each(d.replaceDomain(resp.Request, "href"))
		doc.Find("link").Each(d.replaceDomain(resp.Request, "href"))

		/*
		replace domain in <script src=""></script>, <style src=""></style>
		 */
		doc.Find("link").Each(d.replaceDomain(resp.Request, "src"))
		doc.Find("style").Each(d.replaceDomain(resp.Request, "src"))

		/*
		replace slash start url with full domain
		 */
		doc.Find("a").Each(d.updateSlashStart(resp.Request, "href"))

		// TODO replace url in javascript, css style

		str, err := doc.Html()
		if err != nil {
			log.Printf("error: %s", err)
			return nil
		}
		var buffer bytes.Buffer

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
			n, err := w.Write([]byte(str))
			if err != nil {
				return err
			}
			resp.Body = ioutil.NopCloser(&buffer)
			resp.ContentLength = int64(n)
		case "deflate":
			w, _ := flate.NewWriter(&buffer, 1)
			n, err := w.Write([]byte(str))
			if err != nil {
				return err
			}
			resp.Body = ioutil.NopCloser(&buffer)
			resp.ContentLength = int64(n)
		default:
			resp.Body = ioutil.NopCloser(strings.NewReader(str))
			l := len([]byte(str))
			resp.ContentLength = int64(l)
			resp.Header.Set("Content-Length", strconv.Itoa(l))
			delete(resp.Header, "Content-Encoding")
		}

	}
	return nil
}

// TODO update header for 302 301
func (d *Director) updateHeader(req *http.Request, resp *http.Response) {

}

func (d *Director) updateSlashStart(req *http.Request, attrName string) func(i int, s *goquery.Selection) {
	return func(i int, s *goquery.Selection) {
		attr, exist := s.Attr(attrName)
		if !exist {
			return
		}
		if strings.HasPrefix(attr, "/") && ! strings.HasPrefix(attr, "//") {
			log.Printf("slash start %s: %#v", attrName, attr)
			// update target domain
			if req.Host == d.target.Host {
				s.SetAttr(attrName, d.schema+"://"+d.domain+attr)
				return
			}

			// update subdomain
			for _, domain := range d.subdomains {
				if domain == req.Host {
					log.Printf("attr: %#v, req.URL.Path: %#v", attr, req.URL)
					// http://127.0.0.1/{domainPrefix}/{schema}/{domain}/
					s.SetAttr(attrName, d.schema+"://"+d.domain+d.domainPrefix+req.URL.Scheme+"/"+domain+attr)
					break
				}
			}
		}
	}
}

func (d *Director) replaceDomain(req *http.Request, attrName string) func(i int, s *goquery.Selection) {
	return func(i int, s *goquery.Selection) {
		attr, exist := s.Attr(attrName)
		if !exist {
			return
		}
		log.Printf("replace domain in %s: %#v", attrName, attr)

		// target host
		if strings.HasPrefix(attr, "https://"+d.target.Host+"/") ||
			strings.HasPrefix(attr, "http://"+d.target.Host+"/") ||
			strings.HasPrefix(attr, "//"+d.target.Host) {
			s.SetAttr(attrName, strings.Replace(attr, d.target.Host, d.domain, 1))
			return
		}

		// sub domain
		for _, domain := range d.subdomains {
			if strings.HasPrefix(attr, "https://"+domain+"/") {
				s.SetAttr(attrName,
					strings.Replace(attr, "https://"+domain,
						d.schema+"://"+d.domain+"/x/https/"+domain, 1))
				break
			} else if strings.HasPrefix(attr, "//"+domain+"/") {
				s.SetAttr(attrName,
					strings.Replace(attr, "//"+domain,
						d.schema+"://"+d.domain+"/x/"+req.URL.Scheme+"/"+domain, 1))
				break
			} else if strings.HasPrefix(attr, "http://"+domain+"/") {
				s.SetAttr(attrName,
					strings.Replace(attr, "http://"+domain,
						d.schema+"://"+d.domain+"/x/http/"+domain, 1))
				break
			}
		}
	}
}
