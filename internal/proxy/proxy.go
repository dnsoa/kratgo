package proxy

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/savsgio/kratgo/internal/cache"
	"github.com/savsgio/kratgo/internal/invalidator"
	"github.com/savsgio/kratgo/internal/proxy/config"

	logger "github.com/savsgio/go-logger"
	"github.com/savsgio/govaluate/v3"
	"github.com/valyala/fasthttp"
)

// New ...
func New(cfg config.Config) (*Proxy, error) {
	p := new(Proxy)

	logOutput, err := getLogOutput(cfg.LogOutput)
	if err != nil {
		return nil, err
	}

	log := logger.New("kratgo", cfg.LogLevel, logOutput)

	s := &fasthttp.Server{
		Handler: p.handler,
		Name:    "Kratgo",
		Logger:  log,
	}

	cacheVerbose := cfg.LogLevel == logger.DEBUG
	cacheCleanFrequency := cfg.Cache.CleanFrequency
	if cacheCleanFrequency == 0 {
		return nil, fmt.Errorf("Cache.CleanFrequency configuration must be greater than 0")
	}

	c, err := cache.New(cache.Config{
		TTL:              cfg.Cache.TTL * time.Minute,
		CleanFrequency:   cacheCleanFrequency * time.Minute,
		MaxEntries:       cfg.Cache.MaxEntries,
		MaxEntrySize:     cfg.Cache.MaxEntrySize,
		HardMaxCacheSize: cfg.Cache.HardMaxCacheSize,
		Verbose:          cacheVerbose,
		LogLevel:         cfg.LogLevel,
		LogOutput:        logOutput,
	})
	if err != nil {
		return nil, err
	}

	i := invalidator.New(invalidator.Config{
		Addr:       cfg.Invalidator.Addr,
		Cache:      c,
		MaxWorkers: cfg.Invalidator.MaxWorkers,
		LogLevel:   cfg.LogLevel,
		LogOutput:  logOutput,
	})

	p.backends = make([]fetcher, len(cfg.Proxy.BackendsAddrs))
	for i, addr := range cfg.Proxy.BackendsAddrs {
		p.backends[i] = &fasthttp.HostClient{
			Addr: addr,
		}
	}
	p.totalBackends = len(cfg.Proxy.BackendsAddrs)

	p.server = s
	p.cache = c
	p.invalidator = i
	p.httpScheme = defaultHTTPScheme
	p.log = log
	p.cfg = cfg

	p.tools = sync.Pool{
		New: func() interface{} {
			return &proxyTools{
				httpClient: acquireHTTPClient(),
				params:     acquireEvalParams(),
				entry:      cache.AcquireEntry(),
			}
		},
	}

	if err = p.parseNocacheRules(); err != nil {
		return nil, err
	}

	if err = p.parseHeadersRules(setHeaderAction, p.cfg.Proxy.Response.Headers.Set); err != nil {
		return nil, err
	}

	if err = p.parseHeadersRules(unsetHeaderAction, p.cfg.Proxy.Response.Headers.Unset); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Proxy) acquireTools() *proxyTools {
	return p.tools.Get().(*proxyTools)
}

func (p *Proxy) releaseTools(pt *proxyTools) {
	pt.httpClient.reset()
	pt.params.reset()
	pt.entry.Reset()

	p.tools.Put(pt)
}

func (p *Proxy) getBackend() fetcher {
	if p.totalBackends == 1 {
		return p.backends[0]
	}

	p.mu.Lock()

	if p.currentBackend >= p.totalBackends-1 {
		p.currentBackend = 0
	} else {
		p.currentBackend++
	}

	backend := p.backends[p.currentBackend]

	p.mu.Unlock()

	return backend
}

func (p *Proxy) newEvaluableExpression(rule string) (*govaluate.EvaluableExpression, []ruleParam, error) {
	params := make([]ruleParam, 0)

	for config.ConfigVarRegex.MatchString(rule) {
		configKey, evalKey, evalSubKey := config.ParseConfigKeys(rule)
		if configKey == "" && evalKey == "" && evalSubKey == "" {
			return nil, nil, fmt.Errorf("Invalid condition: %s", rule)
		}

		rule = strings.Replace(rule, configKey, evalKey, -1)
		params = append(params, ruleParam{name: evalKey, subKey: evalSubKey})
	}

	expr, err := govaluate.NewEvaluableExpression(rule)
	return expr, params, err
}

func (p *Proxy) parseNocacheRules() error {
	for _, ncRule := range p.cfg.Proxy.Nocache {
		r := rule{}

		expr, params, err := p.newEvaluableExpression(ncRule)
		if err != nil {
			return fmt.Errorf("Could not get the evaluable expression for rule '%s': %v", ncRule, err)
		}
		r.expr = expr
		r.params = append(r.params, params...)

		p.nocacheRules = append(p.nocacheRules, r)
	}

	return nil
}

func (p *Proxy) parseHeadersRules(action string, headers []config.Header) error {
	for _, h := range headers {
		r := headerRule{action: action, name: h.Name}

		if h.When != "" {
			expr, params, err := p.newEvaluableExpression(h.When)
			if err != nil {
				return fmt.Errorf("Could not get the evaluable expression for rule '%s': %v", h.When, err)
			}
			r.expr = expr
			r.params = append(r.params, params...)
		}

		if action == setHeaderAction {
			_, evalKey, evalSubKey := config.ParseConfigKeys(h.Value)
			if evalKey != "" {
				r.value.value = evalKey
				r.value.subKey = evalSubKey
			} else {
				r.value.value = h.Value
			}
		}

		p.headersRules = append(p.headersRules, r)
	}

	return nil
}

func (p *Proxy) saveBackendResponse(cacheKey, path []byte, resp *fasthttp.Response, entry *cache.Entry) error {
	r := cache.AcquireResponse()

	r.Path = append(r.Path, path...)
	r.Body = append(r.Body, resp.Body()...)
	resp.Header.VisitAll(func(k, v []byte) {
		r.SetHeader(k, v)
	})

	entry.SetResponse(*r)

	if err := p.cache.SetBytes(cacheKey, entry); err != nil {
		return fmt.Errorf("Could not save response in cache for key '%s': %v", cacheKey, err)
	}

	cache.ReleaseResponse(r)

	return nil
}

func (p *Proxy) fetchFromBackend(cacheKey, path []byte, ctx *fasthttp.RequestCtx, pt *proxyTools) error {
	if p.log.DebugEnabled() {
		p.log.Debugf("%s - %s", ctx.Method(), ctx.Path())
	}

	cloneHeaders(&pt.httpClient.req.Header, &ctx.Request.Header)
	pt.httpClient.setMethodBytes(ctx.Method())
	pt.httpClient.setRequestURIBytes(path)

	if err := pt.httpClient.do(p.getBackend()); err != nil {
		return fmt.Errorf("Could not fetch response from backend: %v", err)
	}

	if err := pt.httpClient.processHeaderRules(p.headersRules, pt.params); err != nil {
		return fmt.Errorf("Could not process headers rules: %v", err)
	}
	pt.httpClient.copyReqHeaderTo(&ctx.Request.Header)
	pt.httpClient.copyRespHeaderTo(&ctx.Response.Header)

	location := pt.httpClient.respHeaderPeek(headerLocation)
	if len(location) > 0 {
		return nil
	}

	noCache, err := checkIfNoCache(pt.httpClient.req, pt.httpClient.resp, p.nocacheRules, pt.params)
	if err != nil {
		return err
	}

	ctx.SetStatusCode(pt.httpClient.statusCode())
	ctx.SetBody(pt.httpClient.body())

	if noCache || ctx.Response.StatusCode() != fasthttp.StatusOK {
		return nil
	}

	return p.saveBackendResponse(cacheKey, path, &ctx.Response, pt.entry)
}

func (p *Proxy) handler(ctx *fasthttp.RequestCtx) {
	pt := p.acquireTools()

	path := ctx.URI().PathOriginal()
	cacheKey := ctx.Host()

	if noCache, err := checkIfNoCache(&ctx.Request, &ctx.Response, p.nocacheRules, pt.params); err != nil {
		ctx.Error(err.Error(), fasthttp.StatusInternalServerError)
		p.log.Error(err)

	} else if !noCache {
		if err := p.cache.GetBytes(cacheKey, pt.entry); err != nil {
			ctx.Error(err.Error(), fasthttp.StatusInternalServerError)
			p.log.Errorf("Could not get data from cache with key '%s': %v", cacheKey, err)

		} else if r := pt.entry.GetResponse(path); r != nil {
			ctx.SetBody(r.Body)
			for _, h := range r.Headers {
				ctx.Response.Header.SetCanonical(h.Key, h.Value)
			}

			p.releaseTools(pt)
			return
		}
	}

	if err := p.fetchFromBackend(cacheKey, path, ctx, pt); err != nil {
		ctx.Error(err.Error(), fasthttp.StatusInternalServerError)
		p.log.Error(err)
	}

	p.releaseTools(pt)
}

// ListenAndServe ...
func (p *Proxy) ListenAndServe() error {
	defer p.logFile.Close()

	go p.invalidator.Start()

	p.log.Infof("Listening on: %s://%s/", p.httpScheme, p.cfg.Proxy.Addr)

	return p.server.ListenAndServe(p.cfg.Proxy.Addr)
}
