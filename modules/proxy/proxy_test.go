package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	logger "github.com/savsgio/go-logger/v4"
	"github.com/savsgio/gotils/strconv"
	"github.com/savsgio/kratgo/modules/cache"
	"github.com/savsgio/kratgo/modules/config"
	"github.com/valyala/fasthttp"
)

type mockServer struct {
	addr                 string
	listenAndServeCalled bool

	mu sync.RWMutex
}

type mockBackend struct {
	called bool

	body       []byte
	headers    map[string][]byte
	statusCode int
	err        error
}

func (mock *mockBackend) Do(req *fasthttp.Request, resp *fasthttp.Response) error {
	mock.called = true

	resp.SetBody(mock.body)
	resp.SetStatusCode(mock.statusCode)

	for k, v := range mock.headers {
		resp.Header.SetCanonical(strconv.S2B(k), v)
	}

	return mock.err
}

var testCache *cache.Cache

func init() {
	c, err := cache.New(cache.Config{
		FileConfig: config.Cache{
			TTL:              10,
			CleanFrequency:   5,
			MaxEntries:       5,
			MaxEntrySize:     20,
			HardMaxCacheSize: 30,
		},
		LogLevel:  logger.ERROR,
		LogOutput: os.Stderr,
	})
	if err != nil {
		panic(err)
	}

	testCache = c
}

func (mock *mockServer) ListenAndServe(addr string) error {
	mock.mu.Lock()
	mock.addr = addr
	mock.listenAndServeCalled = true
	mock.mu.Unlock()

	time.Sleep(250 * time.Millisecond)

	return nil
}

func testConfig() Config {
	testCache.Reset()

	return Config{
		FileConfig: config.Proxy{
			Addr:         "localhost:8000",
			BackendAddrs: []string{"localhost:9990", "localhost:9991", "localhost:9993", "localhost:9994"},
		},
		Cache:     testCache,
		LogLevel:  logger.FATAL,
		LogOutput: os.Stderr,
	}
}

func TestProxy_New(t *testing.T) {
	type args struct {
		cfg Config
	}

	type want struct {
		err bool
	}

	logLevel := logger.FATAL
	logOutput := os.Stderr
	httpScheme := "http"

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "Ok",
			args: args{
				cfg: Config{
					FileConfig: config.Proxy{
						Addr:         "localhost:9999",
						BackendAddrs: []string{"localhost:8881", "localhost:8882"},
						Response: config.ProxyResponse{
							Headers: config.ProxyResponseHeaders{
								Set: []config.Header{
									{Name: "X-Kratgo", Value: "true", When: "$(resp.header::X-Data) == '1'"},
								},
								Unset: []config.Header{
									{Name: "X-Data", When: "$(resp.header::X-Data) == '1'"},
								},
							},
						},
						Nocache: []string{"$(host) == 'localhost'"},
					},
					Cache:      testCache,
					HTTPScheme: httpScheme,
					LogLevel:   logLevel,
					LogOutput:  logOutput,
				},
			},
			want: want{
				err: false,
			},
		},
		{
			name: "ErrorNoBackendAddrs",
			args: args{
				cfg: Config{
					FileConfig: config.Proxy{
						Addr:         "localhost:9999",
						BackendAddrs: []string{},
						Response: config.ProxyResponse{
							Headers: config.ProxyResponseHeaders{
								Set: []config.Header{
									{Name: "X-Kratgo", Value: "true", When: "$(resp.header::X-Data) == '1'"},
								},
								Unset: []config.Header{
									{Name: "X-Data", When: "$(resp.header::X-Data) == '1'"},
								},
							},
						},
						Nocache: []string{"$(host) == 'localhost'"},
					},
					Cache:      testCache,
					HTTPScheme: httpScheme,
					LogLevel:   logLevel,
					LogOutput:  logOutput,
				},
			},
			want: want{
				err: true,
			},
		},
		{
			name: "ErrorParseNoCacheRules",
			args: args{
				cfg: Config{
					FileConfig: config.Proxy{
						Addr:         "localhost:9999",
						BackendAddrs: []string{"localhost:8881", "localhost:8882"},
						Response: config.ProxyResponse{
							Headers: config.ProxyResponseHeaders{
								Set: []config.Header{
									{Name: "X-Kratgo", Value: "true", When: "$(resp.header::X-Data) == '1'"},
								},
								Unset: []config.Header{
									{Name: "X-Data", When: "$(resp.header::X-Data) == '1'"},
								},
							},
						},
						Nocache: []string{"$(fake) == 'localhost'"},
					},
					Cache:      testCache,
					HTTPScheme: httpScheme,
					LogLevel:   logLevel,
					LogOutput:  logOutput,
				},
			},
			want: want{
				err: true,
			},
		},
		{
			name: "ErrorParseHeaderRulesSet",
			args: args{
				cfg: Config{
					FileConfig: config.Proxy{
						Addr:         "localhost:9999",
						BackendAddrs: []string{"localhost:8881", "localhost:8882"},
						Response: config.ProxyResponse{
							Headers: config.ProxyResponseHeaders{
								Set: []config.Header{
									{Name: "X-Kratgo", Value: "true", When: "$(fake::X-Data) == '1'"},
								},
								Unset: []config.Header{
									{Name: "X-Data", When: "$(resp.header::X-Data) == '1'"},
								},
							},
						},
						Nocache: []string{"$(host) == 'localhost'"},
					},
					Cache:      testCache,
					HTTPScheme: httpScheme,
					LogLevel:   logLevel,
					LogOutput:  logOutput,
				},
			},
			want: want{
				err: true,
			},
		},
		{
			name: "ErrorParseHeaderRulesUnset",
			args: args{
				cfg: Config{
					FileConfig: config.Proxy{
						Addr:         "localhost:9999",
						BackendAddrs: []string{"localhost:8881", "localhost:8882"},
						Response: config.ProxyResponse{
							Headers: config.ProxyResponseHeaders{
								Set: []config.Header{
									{Name: "X-Kratgo", Value: "true", When: "$(resp.header::X-Data) == '1'"},
								},
								Unset: []config.Header{
									{Name: "X-Data", When: "$(fake::X-Data) == '1'"},
								},
							},
						},
						Nocache: []string{"$(host) == 'localhost'"},
					},
					Cache:      testCache,
					HTTPScheme: httpScheme,
					LogLevel:   logLevel,
					LogOutput:  logOutput,
				},
			},
			want: want{
				err: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.args.cfg)
			if (err != nil) != tt.want.err {
				t.Fatalf("New() error == '%v', want '%v'", err, tt.want.err)
			}

			if tt.want.err {
				return
			}

			if !reflect.DeepEqual(p.fileConfig, tt.args.cfg.FileConfig) {
				t.Errorf("New() fileConfig == '%v', want '%v'", p.fileConfig, tt.args.cfg.FileConfig)
			}

			if p.log == nil {
				t.Errorf("New() fileConfig is '%v'", nil)
			}

			if p.cache == nil {
				t.Errorf("New() cache is '%v'", nil)
			}

			if p.httpScheme != httpScheme {
				t.Errorf("New() httpScheme == '%v', want '%v'", p.httpScheme, httpScheme)
			}

			totalBackends := len(tt.args.cfg.FileConfig.BackendAddrs)
			if len(p.backends) != len(tt.args.cfg.FileConfig.BackendAddrs) {
				t.Errorf("New() backends == '%v', want '%v'", p.backends, tt.args.cfg.FileConfig.BackendAddrs)
			}

			if p.totalBackends != totalBackends {
				t.Errorf("New() totalBackends == '%v', want '%v'", p.totalBackends, totalBackends)
			}

			if p.tools.New == nil {
				t.Errorf("New() tools.New is '%v'", nil)
			}
		})
	}
}

func TestProxy_getBackend(t *testing.T) {
	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	var prevBackend fetcher
	for i := 0; i < len(p.backends)*3; i++ {
		backend := p.getBackend()

		if p.totalBackends == 1 {
			if prevBackend != nil && backend != prevBackend {
				t.Errorf("Proxy.getBackend() returns other backend, current '%p', previous '%p'", backend, prevBackend)
			}
		} else {
			if backend == prevBackend {
				t.Errorf("Proxy.getBackend() returns same backend, current '%p', previous '%p'", backend, prevBackend)
			}
		}

		prevBackend = backend
	}
}

func TestProxy_newEvaluableExpression(t *testing.T) {
	type args struct {
		rule string
	}

	type want struct {
		strExpr   string
		regexExpr *regexp.Regexp
		params    []ruleParam
		err       bool
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "method",
			args: args{
				rule: fmt.Sprintf("$(method) == '%s'", "GET"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s'", config.EvalMethodVar, "GET"),
				params:  []ruleParam{{name: config.EvalMethodVar, subKey: ""}},
				err:     false,
			},
		},
		{
			name: "host",
			args: args{
				rule: fmt.Sprintf("$(host) == '%s'", "www.kratgo.com"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s'", config.EvalHostVar, "www.kratgo.com"),
				params:  []ruleParam{{name: config.EvalHostVar, subKey: ""}},
				err:     false,
			},
		},
		{
			name: "path",
			args: args{
				rule: fmt.Sprintf("$(path) == '%s'", "/es/"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s'", config.EvalPathVar, "/es/"),
				params:  []ruleParam{{name: config.EvalPathVar, subKey: ""}},
				err:     false,
			},
		},
		{
			name: "contentType",
			args: args{
				rule: fmt.Sprintf("$(contentType) == '%s'", "text/html"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s'", config.EvalContentTypeVar, "text/html"),
				params:  []ruleParam{{name: config.EvalContentTypeVar, subKey: ""}},
				err:     false,
			},
		},
		{
			name: "statusCode",
			args: args{
				rule: fmt.Sprintf("$(statusCode) == '%s'", "200"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s'", config.EvalStatusCodeVar, "200"),
				params:  []ruleParam{{name: config.EvalStatusCodeVar, subKey: ""}},
				err:     false,
			},
		},
		{
			name: "req.header::<NAME>",
			args: args{
				rule: fmt.Sprintf("$(req.header::X-Data) == '%s'", "Kratgo"),
			},
			want: want{
				regexExpr: regexp.MustCompile(fmt.Sprintf("%s([0-9]{1,2}) == '%s'", config.EvalReqHeaderVar, "Kratgo")),
				params:    []ruleParam{{name: config.EvalReqHeaderVar, subKey: "X-Data"}},
				err:       false,
			},
		},
		{
			name: "resp.header::<NAME>",
			args: args{
				rule: fmt.Sprintf("$(resp.header::X-Resp-Data) == '%s'", "Kratgo"),
			},
			want: want{
				regexExpr: regexp.MustCompile(fmt.Sprintf("%s([0-9]{1,2}) == '%s'", config.EvalRespHeaderVar, "Kratgo")),
				params:    []ruleParam{{name: config.EvalRespHeaderVar, subKey: "X-Resp-Data"}},
				err:       false,
			},
		},
		{
			name: "cookie::<NAME>",
			args: args{
				rule: fmt.Sprintf("$(cookie::X-Cookie-Data) == '%s'", "Kratgo"),
			},
			want: want{
				regexExpr: regexp.MustCompile(fmt.Sprintf("%s([0-9]{1,2}) == '%s'", config.EvalCookieVar, "Kratgo")),
				params:    []ruleParam{{name: config.EvalCookieVar, subKey: "X-Cookie-Data"}},
				err:       false,
			},
		},
		{
			name: "combo",
			args: args{
				rule: fmt.Sprintf("$(path) == '%s' && $(method) != '%s'", "/kratgo", "GET"),
			},
			want: want{
				strExpr: fmt.Sprintf("%s == '%s' && %s != '%s'", config.EvalPathVar, "/kratgo", config.EvalMethodVar, "GET"),
				params: []ruleParam{
					{name: config.EvalPathVar, subKey: ""},
					{name: config.EvalMethodVar, subKey: ""},
				},
				err: false,
			},
		},
		{
			name: "Error",
			args: args{
				rule: "$(test) /() thod) != asdasd3'",
			},
			want: want{
				err: true,
			},
		},
	}

	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, params, err := p.newEvaluableExpression(tt.args.rule)

			if (tt.want.err && err == nil) || (!tt.want.err && err != nil) {
				t.Fatalf("Proxy.newEvaluableExpression() returns error '%v', want error '%v'", err, tt.want.err)
			}

			if !tt.want.err {
				strExpr := expr.String()
				if tt.want.regexExpr != nil {
					if !tt.want.regexExpr.MatchString(strExpr) {
						t.Errorf("Proxy.newEvaluableExpression() = '%s', want '%s'", strExpr, tt.want.regexExpr.String())
					}
				} else {
					if strExpr != tt.want.strExpr {
						t.Errorf("Proxy.newEvaluableExpression() = '%s', want '%s'", expr.String(), tt.want.strExpr)
					}
				}

				for _, ruleParam := range params {
					for _, wantParam := range tt.want.params {
						if tt.want.regexExpr != nil {
							if strings.HasPrefix(ruleParam.name, wantParam.name) && wantParam.subKey == ruleParam.subKey {
								goto next
							}
						} else {
							if wantParam.name == ruleParam.name && wantParam.subKey == ruleParam.subKey {
								goto next
							}
						}
					}
					t.Errorf("Proxy.newEvaluableExpression() unexpected parameter %v", ruleParam)

				next:
				}
			}

		})
	}
}

func TestProxy_parseNocacheRules(t *testing.T) {
	type args struct {
		rules []string
	}

	type want struct {
		err bool
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "Ok",
			args: args{
				rules: []string{
					"$(req.header::X-Requested-With) == 'XMLHttpRequest'",
					"$(host) == 'www.kratgo.es' || $(req.header::X-Data) != 'Kratgo'",
				},
			},
			want: want{
				err: false,
			},
		},
		{
			name: "Error",
			args: args{
				rules: []string{
					"$(fake::X-Requested-With) == 'XMLHttpRequest'",
				},
			},
			want: want{
				err: true,
			},
		},
	}

	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.fileConfig.Nocache = tt.args.rules

			err := p.parseNocacheRules()
			if (err != nil) != tt.want.err {
				t.Errorf("Proxy.parseNocacheRules() Unexpected error: %v", err)
			}

			if tt.want.err {
				return
			}

			if len(p.fileConfig.Nocache) != len(p.nocacheRules) {
				t.Errorf("Proxy.parseNocacheRules() parsed %d rules, want %d", len(p.fileConfig.Nocache), len(p.nocacheRules))
			}
		})
	}
}

func TestProxy_parseHeadersRules(t *testing.T) {
	type args struct {
		action typeHeaderAction
		rules  []config.Header
	}

	type want struct {
		action typeHeaderAction
		err    bool
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "Set",
			args: args{
				action: setHeaderAction,
				rules: []config.Header{
					{
						Name:  "X-Data",
						Value: "Kratgo",
						When:  "$(path) == '/kratgo'",
					},
					{
						Name:  "X-Data",
						Value: "$(req.header::X-Data)",
					},
				},
			},
			want: want{
				action: setHeaderAction,
				err:    false,
			},
		},
		{
			name: "Unset",
			args: args{
				action: unsetHeaderAction,
				rules: []config.Header{
					{
						Name: "X-Data",
						When: "$(path) == '/kratgo'",
					},
					{
						Name: "X-Data",
					},
				},
			},
			want: want{
				action: unsetHeaderAction,
				err:    false,
			},
		},
		{
			name: "Error",
			args: args{
				action: unsetHeaderAction,
				rules: []config.Header{
					{
						Name: "X-Data",
						When: "$(fake) == /kratgo",
					},
				},
			},
			want: want{
				err: true,
			},
		},
	}

	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		p.headersRules = p.headersRules[:0]

		t.Run(tt.name, func(t *testing.T) {
			err = p.parseHeadersRules(tt.args.action, tt.args.rules)
			if (err != nil) != tt.want.err {
				t.Fatalf("Proxy.parseHeadersRules() Unexpected error: %v", err)
			}

			if tt.want.err {
				return
			}

			if len(tt.args.rules) != len(p.headersRules) {
				t.Errorf("Proxy.parseHeadersRules() parsed %d rules, want %d", len(p.headersRules), len(tt.args.rules))
			}

			for i, pr := range p.headersRules {
				if tt.want.action != pr.action {
					t.Errorf("Proxy.parseHeadersRules() action == '%d', want '%d'", pr.action, tt.want.action)
				}

				configHeader := tt.args.rules[i]
				if configHeader.When != "" && pr.expr == nil {
					t.Errorf("Proxy.parseHeadersRules() Proxy.headersRules.When '%s' has not be parsed", configHeader.When)
				}

				if configHeader.Name != pr.name {
					t.Errorf("Proxy.parseHeadersRules() name == '%s', want '%s'", configHeader.Name, pr.name)
				}

				_, evalKey, evalSubKey := config.ParseConfigKeys(configHeader.Value)
				if evalKey != "" {
					if !regexp.MustCompile(fmt.Sprintf("%s([0-9]{1,2})", config.EvalReqHeaderVar)).MatchString(evalKey) {
						t.Errorf("Proxy.parseHeadersRules() value.value == '%s', want '%s'", pr.value.value, evalKey)
					}

					if evalSubKey != pr.value.subKey {
						t.Errorf("Proxy.parseHeadersRules() value.subKey == '%s', want '%s'", pr.value.subKey, evalSubKey)
					}
				} else {
					if configHeader.Value != pr.value.value {
						t.Errorf("Proxy.parseHeadersRules() value == '%s', want '%s'", pr.value.value, configHeader.Value)
					}
				}
			}
		})
	}
}

func TestProxy_saveBackendResponse(t *testing.T) {
	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	cacheKey := []byte("test")
	path := []byte("/test/")
	body := []byte("Test Body")
	headers := map[string][]byte{
		"X-Data":   []byte("1"),
		"X-Data-2": []byte("2"),
		"X-Data-3": []byte("3"),
	}
	entry := cache.AcquireEntry()

	resp := fasthttp.AcquireResponse()
	resp.SetBody(body)
	for k, v := range headers {
		resp.Header.SetCanonical([]byte(k), v)
	}

	err = p.saveBackendResponse(cacheKey, path, resp, entry)
	if err != nil {
		t.Fatalf("Proxy.saveBackendResponse() returns err: %v", err)
	}

	entry.Reset()
	err = p.cache.GetBytes(cacheKey, entry)
	if err != nil {
		t.Fatal(err)
	}

	r := entry.GetResponse(path)
	if r == nil {
		t.Fatalf("Proxy.saveBackendResponse() path '%s' not found in cache", path)
	}

	if !bytes.Equal(r.Body, body) {
		t.Fatalf("Proxy.saveBackendResponse() cache body == '%s', want '%s'", r.Body, body)
	}

	for k, v := range headers {
		for _, h := range r.Headers {
			if string(h.Key) == k && bytes.Equal(h.Value, v) {
				goto next
			}
		}
		t.Errorf("Proxy.saveBackendResponse() header '%s=%s' not found in cache", k, v)

	next:
	}
}

func TestProxy_fetchFromBackend(t *testing.T) {
	type args struct {
		cacheKey     []byte
		path         []byte
		body         []byte
		method       []byte
		headers      map[string][]byte
		statusCode   int
		noCacheRules []string
		headersRules []config.Header

		httpClientError              error
		forceProcessHeaderRulesError bool
		forceCheckIfNoCacheError     bool
	}

	type want struct {
		saveInCache bool
		err         bool
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "StatusOk",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("POST"),
				headers: map[string][]byte{
					"X-Data":   []byte("1"),
					"X-Data-2": []byte("2"),
					"X-Data-3": []byte("3"),
				},
				statusCode: 200,
			},
			want: want{
				saveInCache: true,
				err:         false,
			},
		},
		{
			name: "StatusRedirect",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					headerLocation: []byte("http://www.kratgo.com"),
				},
				statusCode: 301,
			},
			want: want{
				saveInCache: false,
				err:         false,
			},
		},
		{
			name: "NoCacheByRule",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					"X-Data": []byte("1"),
				},
				statusCode: 200,
				noCacheRules: []string{
					"$(path) == '/test/'",
				},
			},
			want: want{
				saveInCache: false,
				err:         false,
			},
		},
		{
			name: "NoCacheByStatusCode",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					"X-Data": []byte("1"),
				},
				statusCode: 404,
			},
			want: want{
				saveInCache: false,
				err:         false,
			},
		},
		{
			name: "ErrorHttpClientDo",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					"X-Data": []byte("1"),
				},
				statusCode:      404,
				httpClientError: errors.New("Error"),
			},
			want: want{
				saveInCache: false,
				err:         true,
			},
		},
		{
			name: "ErrorParseHeaderRules",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					"X-Data": []byte("1"),
				},
				statusCode: 404,
				headersRules: []config.Header{
					{Name: "X-Data", Value: "1", When: "$(path) == '/kratgo'"},
				},
				forceProcessHeaderRulesError: true,
			},
			want: want{
				saveInCache: false,
				err:         true,
			},
		},
		{
			name: "ErrorCheckIfNoCache",
			args: args{
				cacheKey: []byte("test"),
				path:     []byte("/test/"),
				body:     []byte("Test Body"),
				method:   []byte("GET"),
				headers: map[string][]byte{
					"X-Data": []byte("1"),
				},
				statusCode: 404,
				noCacheRules: []string{
					"$(path) == '/test/'",
				},
				forceCheckIfNoCacheError: true,
			},
			want: want{
				saveInCache: false,
				err:         true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.FileConfig.Nocache = tt.args.noCacheRules
			cfg.FileConfig.Response.Headers.Set = tt.args.headersRules

			p, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}

			if tt.args.forceProcessHeaderRulesError {
				p.headersRules[0].params = p.headersRules[0].params[:0]
			}

			if tt.args.forceCheckIfNoCacheError {
				p.nocacheRules[0].params = p.nocacheRules[0].params[:0]
			}

			p.fileConfig.Nocache = tt.args.noCacheRules
			p.backends = []fetcher{
				&mockBackend{
					body:       tt.args.body,
					statusCode: tt.args.statusCode,
					headers:    tt.args.headers,
					err:        tt.args.httpClientError,
				},
			}
			p.totalBackends = len(tt.args.noCacheRules)

			pt := p.acquireTools()
			entry := cache.AcquireEntry()

			ctx := new(fasthttp.RequestCtx)
			ctx.Request.SetRequestURIBytes(tt.args.path)
			ctx.Request.Header.SetMethodBytes(tt.args.method)
			for k, v := range tt.args.headers {
				ctx.Request.Header.SetCanonical([]byte(k), v)
			}

			err = p.fetchFromBackend(tt.args.cacheKey, tt.args.path, ctx, pt)
			if (err != nil) != tt.want.err {
				t.Errorf("Proxy.fetchFromBackend() Unexpected error: %v", err)
			}

			if v := ctx.Request.Header.Peek(proxyReqHeaderKey); string(v) != proxyReqHeaderValue {
				t.Errorf("The header '%s = %s' not found in request", proxyReqHeaderKey, proxyReqHeaderValue)
			}

			if tt.want.err {
				return
			}

			err = p.cache.GetBytes(tt.args.cacheKey, entry)
			if err != nil {
				t.Fatal(err)
			}

			if tt.want.saveInCache {
				r := entry.GetResponse(tt.args.path)
				if r == nil {
					t.Fatalf("Proxy.saveBackendResponse() path '%s' not found in cache", tt.args.path)
				}

				if !bytes.Equal(r.Body, tt.args.body) {
					t.Fatalf("Proxy.saveBackendResponse() cache body == '%s', want '%s'", r.Body, tt.args.body)
				}

				for k, v := range tt.args.headers {
					for _, h := range r.Headers {
						if string(h.Key) == k && bytes.Equal(h.Value, v) {
							goto next
						}
					}
					t.Errorf("Proxy.saveBackendResponse() header '%s=%s' not found in cache", k, v)

				next:
				}
			}
		})
	}
}

func TestProxy_handler(t *testing.T) {
	type args struct {
		host         []byte
		path         []byte
		headers      []cache.ResponseHeader
		cachePath    []byte
		noCacheRules []string

		forceProcessHeaderRulesError bool
		httpClientError              error
	}

	type want struct {
		getFromCache   bool
		getFromBackend bool
		err            bool
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "ResponseFromCache",
			args: args{
				host: []byte("www.kratgo.com"),
				path: []byte("/test/"),
				headers: []cache.ResponseHeader{
					{Key: []byte("X-Key"), Value: []byte("1")},
				},
				cachePath: []byte("/test/"),
			},
			want: want{
				getFromCache:   true,
				getFromBackend: false,
				err:            false,
			},
		},
		{
			name: "ResponseFromCacheNotFound",
			args: args{
				host:      []byte("www.kratgo.com"),
				path:      []byte("/test/"),
				cachePath: []byte("/test/data/"),
			},
			want: want{
				getFromCache:   true,
				getFromBackend: true,
				err:            false,
			},
		},
		{
			name: "ResponseFromBackend",
			args: args{
				host:      []byte("www.kratgo.com"),
				path:      []byte("/test/"),
				cachePath: []byte("/test/data/"),
			},
			want: want{
				getFromCache:   false,
				getFromBackend: true,
				err:            false,
			},
		},
		{
			name: "ResponseFromBackendByNocache",
			args: args{
				host: []byte("www.kratgo.com"),
				noCacheRules: []string{
					"$(host) == 'www.kratgo.com'",
				},
			},
			want: want{
				getFromCache:   false,
				getFromBackend: true,
				err:            false,
			},
		},
		{
			name: "ErrorCheckIfNoCache",
			args: args{
				host: []byte("www.kratgo.com"),
				noCacheRules: []string{
					"$(host) == 'www.kratgo.com'",
				},
				forceProcessHeaderRulesError: true,
			},
			want: want{
				err: true,
			},
		},
		{
			name: "ErrorFetchFromBackend",
			args: args{
				host:            []byte("www.kratgo.com"),
				httpClientError: errors.New("Error"),
			},
			want: want{
				err: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.FileConfig.Nocache = tt.args.noCacheRules

			p, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}

			if tt.args.forceProcessHeaderRulesError {
				p.nocacheRules[0].params = p.nocacheRules[0].params[:0]
			}

			ctx := new(fasthttp.RequestCtx)
			ctx.Request.SetRequestURIBytes(tt.args.path)
			ctx.Request.Header.SetHostBytes(tt.args.host)

			entry := cache.AcquireEntry()
			response := cache.AcquireResponse()
			response.Path = tt.args.cachePath
			for _, h := range tt.args.headers {
				response.SetHeader(h.Key, h.Value)
			}
			entry.SetResponse(*response)
			p.cache.SetBytes(tt.args.host, *entry)

			httpClientMock := &mockBackend{
				statusCode: 200,
				err:        tt.args.httpClientError,
			}
			p.backends = []fetcher{httpClientMock}
			p.totalBackends = len(p.backends)

			p.handler(ctx)

			if (ctx.Response.StatusCode() == fasthttp.StatusInternalServerError) != tt.want.err {
				t.Errorf("Proxy.handler() Unexpected error: %s", ctx.Response.Body())
			}

			if tt.want.err {
				return
			}

			if tt.want.getFromCache {
				if tt.want.getFromBackend && !httpClientMock.called {
					t.Errorf("Proxy.handler() response from backend '%v', want '%v'", false, true)
				} else if !tt.want.getFromBackend && httpClientMock.called {
					t.Errorf("Proxy.handler() response from cache '%v', want '%v'", true, false)
				}

			} else {
				if tt.want.getFromBackend && !httpClientMock.called {
					t.Errorf("Proxy.handler() response from backend '%v', want '%v'", false, true)
				} else if !tt.want.getFromBackend && httpClientMock.called {
					t.Errorf("Proxy.handler() response from backend '%v', want '%v'", false, true)
				}
			}

			for _, h := range tt.args.headers {
				if !bytes.Equal(ctx.Response.Header.PeekBytes(h.Key), h.Value) {
					t.Errorf("Proxy.handler() the header '%s = %s' not found in response", h.Key, h.Value)
				}
			}
		})
	}
}

func TestProxy_ListenAndServe(t *testing.T) {
	serverMock := new(mockServer)
	addr := "localhost:9999"

	p, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	p.fileConfig.Addr = addr
	p.server = serverMock

	p.ListenAndServe()

	serverMock.mu.RLock()
	defer serverMock.mu.RUnlock()
	if !serverMock.listenAndServeCalled {
		t.Error("Proxy.ListenAndServe() invalidator is not start")
	}

	if serverMock.addr != addr {
		t.Errorf("Proxy.ListenAndServe() addr == '%s', want '%s'", serverMock.addr, addr)
	}

}

func BenchmarkHandler(b *testing.B) {
	p, err := New(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	p.backends = []fetcher{
		&mockBackend{
			body:       []byte("Benchmark Response Body"),
			statusCode: 200,
			headers: map[string][]byte{
				"X-Data": []byte("Kratgo"),
			},
		},
	}
	p.totalBackends = len(p.backends)

	ctx := new(fasthttp.RequestCtx)
	ctx.Request.SetRequestURI("/bench")
	ctx.Request.Header.SetMethod("GET")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.handler(ctx)
	}
}

func BenchmarkHandlerWithoutCache(b *testing.B) {
	path := "/bench"
	cfg := testConfig()
	cfg.FileConfig.Nocache = []string{
		fmt.Sprintf("$(path) == '%s'", path),
	}
	cfg.FileConfig.Response.Headers.Set = []config.Header{
		{Name: "X-Data", Value: "1", When: fmt.Sprintf("$(path) == '%s'", path)},
	}

	p, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}

	p.backends = []fetcher{
		&mockBackend{
			body:       []byte("Benchmark Response Body"),
			statusCode: 200,
			headers: map[string][]byte{
				"X-Data": []byte("Kratgo"),
			},
		},
	}
	p.totalBackends = len(p.backends)

	ctx := new(fasthttp.RequestCtx)
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetMethod("GET")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.handler(ctx)
	}
}
