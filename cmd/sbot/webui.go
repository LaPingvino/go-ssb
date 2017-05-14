package main

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/russross/blackfriday"

	"github.com/andyleap/boltinspect"

	"github.com/andyleap/go-ssb"
	"github.com/andyleap/go-ssb/blobs"
	"github.com/andyleap/go-ssb/channels"
	"github.com/andyleap/go-ssb/gossip"
	"github.com/andyleap/go-ssb/graph"
	"github.com/andyleap/go-ssb/search"
	"github.com/andyleap/go-ssb/social"
)

var ContentTemplates = template.New("content")

type SSBRenderer struct {
	blackfriday.Renderer
}

func (ssbr *SSBRenderer) AutoLink(out *bytes.Buffer, link []byte, kind int) {
	r := ssb.ParseRef(string(link))
	switch r.Type {
	case ssb.RefBlob:
		link = []byte("/blob?id=" + url.QueryEscape(r.String()))
	case ssb.RefMessage:
		link = []byte("/post?id=" + url.QueryEscape(r.String()))
	case ssb.RefFeed:
		link = []byte("/feed?id=" + url.QueryEscape(r.String()))
	}
	if link[0] == '#' {
		link = []byte("/channel?channel=" + url.QueryEscape(string(link[1:])))
	}
	ssbr.Renderer.AutoLink(out, link, kind)
}
func (ssbr *SSBRenderer) Link(out *bytes.Buffer, link []byte, title []byte, content []byte) {
	r := ssb.ParseRef(string(link))
	switch r.Type {
	case ssb.RefBlob:
		link = []byte("/blob?id=" + url.QueryEscape(r.String()))
	case ssb.RefMessage:
		link = []byte("/post?id=" + url.QueryEscape(r.String()))
	case ssb.RefFeed:
		link = []byte("/feed?id=" + url.QueryEscape(r.String()))
	}
	if link[0] == '#' {
		link = []byte("/channel?channel=" + url.QueryEscape(string(link[1:])))
	}
	ssbr.Renderer.Link(out, link, title, content)
}

func (ssbr *SSBRenderer) Image(out *bytes.Buffer, link []byte, title []byte, alt []byte) {
	r := ssb.ParseRef(string(link))
	switch r.Type {
	case ssb.RefBlob:
		link = []byte("/blob?id=" + url.QueryEscape(r.String()))
	}
	ssbr.Renderer.Image(out, link, title, alt)
}

func RenderMarkdown(input []byte) []byte {
	commonHtmlFlags := 0 |
		blackfriday.HTML_USE_XHTML |
		blackfriday.HTML_USE_SMARTYPANTS |
		blackfriday.HTML_SMARTYPANTS_FRACTIONS |
		blackfriday.HTML_SMARTYPANTS_DASHES |
		blackfriday.HTML_SMARTYPANTS_LATEX_DASHES

	commonExtensions := 0 |
		blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
		blackfriday.EXTENSION_TABLES |
		blackfriday.EXTENSION_FENCED_CODE |
		blackfriday.EXTENSION_AUTOLINK |
		blackfriday.EXTENSION_STRIKETHROUGH |
		blackfriday.EXTENSION_SPACE_HEADERS |
		blackfriday.EXTENSION_HEADER_IDS |
		blackfriday.EXTENSION_BACKSLASH_LINE_BREAK |
		blackfriday.EXTENSION_DEFINITION_LISTS
	// set up the HTML renderer
	renderer := &SSBRenderer{blackfriday.HtmlRenderer(commonHtmlFlags, "", "")}
	options := blackfriday.Options{
		Extensions: commonExtensions}

	return blackfriday.MarkdownOptions(input, renderer, options)
}

func init() {
	template.Must(ContentTemplates.Funcs(template.FuncMap{
		"Avatar": func(ref ssb.Ref) template.HTML {
			if ref.Type != ssb.RefFeed {
				return ""
			}
			var a *social.About
			datastore.DB().View(func(tx *bolt.Tx) error {
				a = social.GetAbout(tx, ref)
				return nil
			})
			buf := &bytes.Buffer{}
			err := ContentTemplates.ExecuteTemplate(buf, "avatar.tpl", struct {
				About *social.About
				Ref   ssb.Ref
			}{a, ref})
			if err != nil {
				log.Println(err)
			}
			return template.HTML(buf.String())
		},
		"GetAbout": func(ref ssb.Ref) (a *social.About) {
			datastore.DB().View(func(tx *bolt.Tx) error {
				a = social.GetAbout(tx, ref)
				return nil
			})
			return
		},
		"RenderJSTime": func(timestamp float64) string {
			t := time.Unix(0, int64(timestamp*float64(time.Millisecond))).Local()
			return t.Format(time.ANSIC)
		},
		"Markdown": func(markdown string) template.HTML {
			return template.HTML(RenderMarkdown([]byte(markdown)))
		},
		"GetMessage": func(ref ssb.Ref) *ssb.SignedMessage {
			return datastore.Get(nil, ref)
		},
		"GetVotes": func(ref ssb.Ref) (votes []*ssb.SignedMessage) {
			datastore.DB().View(func(tx *bolt.Tx) error {
				votes = social.GetVotes(tx, ref)
				return nil
			})
			return
		},
		"RenderContent": func(m *ssb.SignedMessage, levels int) template.HTML {
			if m == nil {
				return ""
			}
			t, md := m.DecodeMessage()
			if t == "" {
				return template.HTML("<!-- BLANK --!>")
			}
			buf := &bytes.Buffer{}
			tpl := ContentTemplates.Lookup(t + ".tpl")
			if tpl == nil {
				return template.HTML("<!-- " + t + " --!><pre>" + string(m.Encode()) + "</pre>")
			}
			
			err := ContentTemplates.ExecuteTemplate(buf, t+".tpl", struct {
				Message *ssb.SignedMessage
				Content interface{}
				Levels  int
			}{m, md, levels - 1})
			if err != nil {
				log.Println(err)
			}
			return template.HTML("<!-- " + t + " --!>" + buf.String())
		},
	}).ParseGlob("templates/content/*.tpl"))
}

var PageTemplates = template.Must(template.New("index").Funcs(template.FuncMap{
	"RenderContent": func(m *ssb.SignedMessage, levels int) template.HTML {
		t, md := m.DecodeMessage()
		if t == "" {
			return template.HTML("<!-- BLANK --!>")
		}
		tpl := ContentTemplates.Lookup(t + ".tpl")
			if tpl == nil {
				return template.HTML("<!-- " + t + " --!><pre>" + string(m.Encode()) + "</pre>")
			}
		buf := &bytes.Buffer{}
		err := ContentTemplates.ExecuteTemplate(buf, t+".tpl", struct {
			Message *ssb.SignedMessage
			Content interface{}
			Levels  int
		}{m, md, levels - 1})
		if err != nil {
			log.Println(err)
		}
		return template.HTML("<!-- " + t + " --!>" + buf.String())
	},
	"RenderContentTemplate": func(m *ssb.SignedMessage, levels int, tpl string) template.HTML {
		t, md := m.DecodeMessage()
		buf := &bytes.Buffer{}
		err := ContentTemplates.ExecuteTemplate(buf, tpl+".tpl", struct {
			Message *ssb.SignedMessage
			Content interface{}
			Levels  int
		}{m, md, levels - 1})
		if err != nil {
			log.Println(err)
		}
		return template.HTML("<!-- " + t + " --!>" + buf.String())
	},
	"Decode": func(m *ssb.SignedMessage) interface{} {
		_, mb := m.DecodeMessage()
		return mb
	},
}).ParseGlob("templates/pages/*.tpl"))

func init() {
	log.Println(ContentTemplates.DefinedTemplates())
	log.Println(PageTemplates.DefinedTemplates())
}

func RegisterWebui() {
	bi := boltinspect.New(datastore.DB())

	http.HandleFunc("/bolt", bi.InspectEndpoint)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	http.HandleFunc("/", Index)
	http.Handle("/favicon.ico", http.NotFoundHandler())
	http.HandleFunc("/channel", Channel)
	http.HandleFunc("/post", Post)
	http.HandleFunc("/search", Search)
	http.HandleFunc("/publish/post", PublishPost)
	http.HandleFunc("/publish/about", PublishAbout)
	http.HandleFunc("/publish/follow", PublishFollow)
	http.HandleFunc("/gossip/add", GossipAdd)
	http.HandleFunc("/gossip/accept", GossipAccept)

	http.HandleFunc("/feed", FeedPage)
	http.HandleFunc("/thread", ThreadPage)

	http.HandleFunc("/profile", Profile)

	http.HandleFunc("/admin", Admin)
	http.HandleFunc("/rebuild", Rebuild)

	http.HandleFunc("/blob", Blob)
	http.HandleFunc("/blobinfo", BlobInfo)

	http.HandleFunc("/raw", Raw)

	http.HandleFunc("/upload", Upload)

	go http.ListenAndServe("localhost:9823", nil)
}

func Upload(rw http.ResponseWriter, req *http.Request) {
	f, _, err := req.FormFile("upload")
	if err != nil {
		log.Println(err)
		PageTemplates.ExecuteTemplate(rw, "upload.tpl", nil)
		return
	}
	buf, _ := ioutil.ReadAll(f)
	bs := datastore.ExtraData("blobStore").(*blobs.BlobStore)
	ref := bs.Add(buf)
	http.Redirect(rw, req, "/blobinfo?id="+url.QueryEscape(ref.String()), http.StatusFound)
}

func PublishPost(rw http.ResponseWriter, req *http.Request) {
	p := &social.Post{}
	p.Type = "post"
	p.Root = ssb.ParseRef(req.FormValue("root"))
	p.Branch = ssb.ParseRef(req.FormValue("branch"))
	p.Channel = req.FormValue("channel")
	p.Text = req.FormValue("text")
	datastore.GetFeed(datastore.PrimaryRef).PublishMessage(p)
	http.Redirect(rw, req, req.FormValue("returnto"), http.StatusSeeOther)
}

func PublishAbout(rw http.ResponseWriter, req *http.Request) {
	p := &social.About{}
	p.Type = "about"
	p.About = datastore.PrimaryRef
	p.Name = req.FormValue("name")
	f, _, err := req.FormFile("upload")
	if err == nil {
		buf, _ := ioutil.ReadAll(f)
		bs := datastore.ExtraData("blobStore").(*blobs.BlobStore)
		ref := bs.Add(buf)
		p.Image = &social.Image{}
		p.Image.Link = ref
	}
	datastore.GetFeed(datastore.PrimaryRef).PublishMessage(p)
	http.Redirect(rw, req, "/profile", http.StatusSeeOther)
}

func PublishFollow(rw http.ResponseWriter, req *http.Request) {
	feed := ssb.ParseRef(req.FormValue("feed"))
	if feed.Type == ssb.RefInvalid {
		http.Redirect(rw, req, req.FormValue("returnto"), http.StatusSeeOther)
	}
	p := &graph.Contact{}
	p.Type = "contact"
	p.Contact = feed
	following := true
	p.Following = &following
	datastore.GetFeed(datastore.PrimaryRef).PublishMessage(p)
	datastore.GetFeed(datastore.PrimaryRef).PublishMessage(p)
	http.Redirect(rw, req, req.FormValue("returnto"), http.StatusSeeOther)
}

func GossipAdd(rw http.ResponseWriter, req *http.Request) {
	host := req.FormValue("host")
	if host == "" {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	portStr := req.FormValue("port")
	if portStr == "" {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	port, err := strconv.ParseInt(portStr, 10, 64)
	if err != nil {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	key := ssb.ParseRef(req.FormValue("key"))
	if key.Type != ssb.RefFeed {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}

	pub := gossip.Pub{
		Host: host,
		Port: int(port),
		Link: key,
	}
	gossip.AddPub(datastore, pub)

	http.Redirect(rw, req, "/admin", http.StatusSeeOther)
}

func GossipAccept(rw http.ResponseWriter, req *http.Request) {
	invite := req.FormValue("invite")
	if invite == "" {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	parts := strings.Split(invite, "~")
	if len(parts) != 2 {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	addrparts := strings.Split(parts[0], ":")
	if len(addrparts) != 3 {
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	port, err := strconv.ParseInt(addrparts[1], 10, 64)
	if err != nil {
		log.Println(err)
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}
	follow := req.FormValue("follow")

	pub := gossip.Pub{
		Host: addrparts[0],
		Port: int(port),
		Link: ssb.ParseRef(addrparts[2]),
	}

	seed, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		log.Println(err)
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}

	err = gossip.AcceptInvite(datastore, pub, seed)

	if err != nil {
		log.Println(err)
		http.Redirect(rw, req, "/admin", http.StatusSeeOther)
		return
	}

	if follow == "follow" {
		p := &graph.Contact{}
		p.Type = "contact"
		p.Contact = pub.Link
		following := true
		p.Following = &following
		datastore.GetFeed(datastore.PrimaryRef).PublishMessage(p)
	}

	http.Redirect(rw, req, "/admin", http.StatusSeeOther)
}

func Rebuild(rw http.ResponseWriter, req *http.Request) {
	module := req.FormValue("module")
	if module != "" {
		if module == "all" {
			datastore.RebuildAll()
		} else {
			datastore.Rebuild(module)
		}
	}
	http.Redirect(rw, req, "/admin", http.StatusSeeOther)
}

func calcSize(tx *bolt.Tx, b *bolt.Bucket) (size int) {
	b.ForEach(func(k, v []byte) error {
		size += len(k)
		if v == nil {
			size += calcSize(tx, b.Bucket(k))
		} else {
			size += len(v)
		}
		return nil
	})
	return
}

func Admin(rw http.ResponseWriter, req *http.Request) {
	size := map[string]int{}
	datastore.DB().View(func(tx *bolt.Tx) error {
		tx.ForEach(func(k []byte, b *bolt.Bucket) error {
			size[string(k)] = calcSize(tx, b)
			return nil
		})
		return nil
	})

	modules := []string{}
	for module := range ssb.AddMessageHooks {
		modules = append(modules, module)
	}
	err := PageTemplates.ExecuteTemplate(rw, "admin.tpl", struct {
		Modules []string
		DBSize  map[string]int
	}{
		modules,
		size,
	})
	if err != nil {
		log.Println(err)
	}
}

func Index(rw http.ResponseWriter, req *http.Request) {
	distStr := req.FormValue("dist")
	if distStr == "" {
		distStr = "1"
	}
	dist, _ := strconv.ParseInt(distStr, 10, 64)
	var messages []*ssb.SignedMessage
	if dist == 0 {
		f := datastore.GetFeed(datastore.PrimaryRef)
		messages = f.LatestCount(100)
	} else {
		messages = datastore.LatestCountFiltered(100, graph.GetFollows(datastore, datastore.PrimaryRef, int(dist)))
	}
	err := PageTemplates.ExecuteTemplate(rw, "index.tpl", struct {
		Messages []*ssb.SignedMessage
	}{
		messages,
	})
	if err != nil {
		log.Println(err)
	}
}

func FeedPage(rw http.ResponseWriter, req *http.Request) {
	feedRaw := req.FormValue("id")
	distStr := req.FormValue("dist")
	if distStr == "" {
		distStr = "0"
	}
	feed := ssb.ParseRef(feedRaw)
	dist, _ := strconv.ParseInt(distStr, 10, 64)

	var about *social.About
	datastore.DB().View(func(tx *bolt.Tx) error {
		about = social.GetAbout(tx, feed)
		return nil
	})
	var messages []*ssb.SignedMessage
	if dist == 0 {
		f := datastore.GetFeed(feed)
		messages = f.LatestCount(100)
	} else {
		messages = datastore.LatestCountFiltered(100, graph.GetFollows(datastore, feed, int(dist)))
	}
	err := PageTemplates.ExecuteTemplate(rw, "feed.tpl", struct {
		Messages []*ssb.SignedMessage
		Profile  *social.About
		Ref      ssb.Ref
	}{
		messages,
		about,
		feed,
	})
	if err != nil {
		log.Println(err)
	}
}

func ThreadPage(rw http.ResponseWriter, req *http.Request) {
	threadRaw := req.FormValue("id")
	threadRef := ssb.ParseRef(threadRaw)

	root := datastore.Get(nil, threadRef)

	channel := ""

	_, p := root.DecodeMessage()

	if post, ok := p.(*social.Post); ok {
		channel = post.Channel
	}
	var messages []*ssb.SignedMessage
	datastore.DB().View(func(tx *bolt.Tx) error {
		messages = social.GetThread(tx, threadRef)
		return nil
	})

	reply := root.Key()
	if len(messages) > 0 {
		reply = messages[len(messages)-1].Key()
	}

	err := PageTemplates.ExecuteTemplate(rw, "thread.tpl", struct {
		Root     *ssb.SignedMessage
		Channel  string
		Reply    ssb.Ref
		Messages []*ssb.SignedMessage
	}{
		root,
		channel,
		reply,
		messages,
	})
	if err != nil {
		log.Println(err)
	}
}

func Post(rw http.ResponseWriter, req *http.Request) {
	post := req.FormValue("id")
	if post == "" {
		Index(rw, req)
		return
	}
	message := datastore.Get(nil, ssb.ParseRef(post))
	if message == nil {
		http.NotFound(rw, req)
		return
	}
	_, content := message.DecodeMessage()
	p, ok := content.(*social.Post)
	if !ok {
		Index(rw, req)
		return
	}
	var votes []*ssb.SignedMessage
	datastore.DB().View(func(tx *bolt.Tx) error {
		votes = social.GetVotes(tx, message.Key())
		return nil
	})
	err := PageTemplates.ExecuteTemplate(rw, "post.tpl", struct {
		Message *ssb.SignedMessage
		Content *social.Post
		Votes   []*ssb.SignedMessage
	}{
		message,
		p,
		votes,
	})
	if err != nil {
		log.Println(err)
	}
}

func Profile(rw http.ResponseWriter, req *http.Request) {
	var about *social.About
	datastore.DB().View(func(tx *bolt.Tx) error {
		about = social.GetAbout(tx, datastore.PrimaryRef)
		return nil
	})
	err := PageTemplates.ExecuteTemplate(rw, "profile.tpl", struct {
		Profile *social.About
		Ref     ssb.Ref
	}{
		about,
		datastore.PrimaryRef,
	})
	if err != nil {
		log.Println(err)
	}
}

func Channel(rw http.ResponseWriter, req *http.Request) {
	channel := req.FormValue("channel")
	if channel == "" {
		Index(rw, req)
		return
	}
	messages := channels.GetChannelLatest(datastore, channel, 100)
	err := PageTemplates.ExecuteTemplate(rw, "channel.tpl", struct {
		Messages []*ssb.SignedMessage
		Channel  string
	}{
		messages,
		channel,
	})
	if err != nil {
		log.Println(err)
	}
}

func Search(rw http.ResponseWriter, req *http.Request) {
	query := req.FormValue("q")
	if query == "" {
		Index(rw, req)
		return
	}
	if query[0] == '#' {
		http.Redirect(rw, req, "/channel?channel="+query[1:], http.StatusFound)
		return
	}
	r := ssb.ParseRef(query)
	switch r.Type {
	case ssb.RefBlob:
		http.Redirect(rw, req, "/blob?id="+url.QueryEscape(r.String()), http.StatusFound)
		return
	case ssb.RefMessage:
		http.Redirect(rw, req, "/post?id="+url.QueryEscape(r.String()), http.StatusFound)
		return
	case ssb.RefFeed:
		http.Redirect(rw, req, "/feed?id="+url.QueryEscape(r.String()), http.StatusFound)
		return
	}

	messages := search.Search(datastore, query, 50)
	err := PageTemplates.ExecuteTemplate(rw, "search.tpl", struct {
		Messages []*ssb.SignedMessage
	}{
		messages,
	})
	if err != nil {
		log.Println(err)
	}
}

func Blob(rw http.ResponseWriter, req *http.Request) {
	id := req.FormValue("id")
	if id == "" {
		http.NotFound(rw, req)
		return
	}
	r := ssb.ParseRef(id)
	bs := datastore.ExtraData("blobStore").(*blobs.BlobStore)
	if !bs.Has(r) {
		bs.Want(r)
		bs.WaitFor(r)
	}
	rc := bs.Get(r)
	defer rc.Close()
	io.Copy(rw, rc)
}

func BlobInfo(rw http.ResponseWriter, req *http.Request) {
	id := req.FormValue("id")
	if id == "" {
		http.NotFound(rw, req)
		return
	}
	r := ssb.ParseRef(id)
	PageTemplates.ExecuteTemplate(rw, "blob.tpl", struct {
		ID ssb.Ref
	}{
		ID: r,
	})
}

func Raw(rw http.ResponseWriter, req *http.Request) {
	id := req.FormValue("id")
	if id == "" {
		http.NotFound(rw, req)
		return
	}
	r := ssb.ParseRef(id)
	m := datastore.Get(nil, r)
	if m != nil {
		buf := m.Encode()
		rw.Write(buf)
	}
}
