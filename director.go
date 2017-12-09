package anysites

import (
	"strings"
	"net/url"
	"net/http"
	"log"
	"github.com/PuerkitoBio/goquery"
	"bytes"
	"strconv"
	"errors"
	"io/ioutil"
	"compress/gzip"
	"compress/flate"
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
		target: target,
		schema: schema,
		domain: domain,
		domainPrefix: domainPrefifx,
		subdomains: subdomains,
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

func (g *Director) ModifyResponse(resp *http.Response) error {
	ct, ok := resp.Header["Content-Type"]
	if !ok {
		ct, ok = resp.Header["content-type"]
		if !ok {
			return nil
		}
	}
	// log.Printf("response header: %#v", resp.Header)
	if strings.Contains(strings.Join(ct, "; "), "text/html") {

		// TODO update slash start url

		var bodyBytes []byte
		// log.Printf("CE: %s", resp.Header.Get("Content-Encoding"))
		switch resp.Header.Get("Content-Encoding") {
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
		doc.Find("a").Each(g.replaceDomain(resp.Request, "href"))
		doc.Find("link").Each(g.replaceDomain(resp.Request, "href"))

		/*
		replace domain in <script src=""></script>, <style src=""></style>
		 */
		doc.Find("link").Each(g.replaceDomain(resp.Request, "src"))
		doc.Find("style").Each(g.replaceDomain(resp.Request, "src"))

		/*
		replace slash start url with full domain
		 */
		doc.Find("a").Each(g.updateSlashStart(resp.Request, "href"))

		str, err := doc.Html()
		if err != nil {
			log.Printf("error: %s", err)
			return nil
		}
		resp.Body = ioutil.NopCloser(strings.NewReader(str))
		l := len([]byte(str))
		resp.ContentLength = int64(l)
		resp.Header.Set("Content-Length", strconv.Itoa(l))
		delete(resp.Header, "Content-Encoding")
	}
	return nil
}

func (g *Director) updateSlashStart(req *http.Request, attrName string) func(i int, s *goquery.Selection) {
	return func(i int, s *goquery.Selection) {
		attr, exist := s.Attr(attrName)
		if !exist {
			return
		}
		if strings.HasPrefix(attr, "/") && ! strings.HasPrefix(attr, "//") {
			for _, domain := range g.subdomains {
				if domain == req.Host {
					log.Printf("attr: %#v, req.URL.Path: %#v", attr, req.URL)
					// http://127.0.0.1/{domainPrefix}/{schema}/{domain}/
					s.SetAttr(attrName, g.schema+"://"+g.domain+g.domainPrefix+req.URL.Scheme+"/"+domain+attr)
					break
				}
			}
		}
	}
}

func (g *Director) replaceDomain(req *http.Request, attrName string) func(i int, s *goquery.Selection) {
	return func(i int, s *goquery.Selection) {
		attr, exist := s.Attr(attrName)
		if !exist {
			return
		}
		log.Printf("%#v", attr)

		// target host
		if strings.HasPrefix(attr, "https://"+g.target.Host) ||
			strings.HasPrefix(attr, "http://"+g.target.Host) ||
			strings.HasPrefix(attr, "//"+g.target.Host) {
			s.SetAttr(attrName, strings.Replace(attr, g.target.Host, g.domain, 1))
		}

		// sub domain
		for _, domain := range g.subdomains {
			if strings.HasPrefix(attr, "https://"+domain) {
				s.SetAttr(attrName,
					strings.Replace(attr, "https://"+domain,
						g.schema+"://"+g.domain+"/x/https/"+domain, 1))
				break
			} else if strings.HasPrefix(attr, "//"+domain) {
				s.SetAttr(attrName,
					strings.Replace(attr, "//"+domain,
						g.schema+"://"+g.domain+"/x/"+req.URL.Scheme+"/"+domain, 1))
				break
			} else if strings.HasPrefix(attr, "http://"+domain) {
				s.SetAttr(attrName,
					strings.Replace(attr, "http://"+domain,
						g.schema+"://"+g.domain+"/x/http/"+domain, 1))
				break
			}
		}
	}
}
