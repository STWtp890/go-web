// runtimeapitest executes the deployed HTTP acceptance suite.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type result struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence,omitempty"`
}

type tester struct {
	runID       string
	baseURL     string
	frontendURL string
	results     []result
}

type session struct {
	base   string
	client *http.Client
	jar    http.CookieJar
}

type response struct {
	status int
	body   []byte
	header http.Header
}

func main() {
	var baseURL, frontendURL, runID, bootstrapUser, bootstrapPass, reportDir string
	flag.StringVar(&baseURL, "base-url", "http://127.0.0.1:8080", "backend base URL")
	flag.StringVar(&frontendURL, "frontend-url", "http://127.0.0.1:15173", "frontend/Nginx base URL")
	flag.StringVar(&runID, "run-id", time.Now().Format("20060102_150405"), "unique fixture suffix")

	flag.StringVar(&bootstrapUser, "bootstrap-manager", "", "pre-created active manager username")
	flag.StringVar(&bootstrapPass, "bootstrap-password", "", "pre-created active manager password")
	flag.StringVar(&reportDir, "report-dir", "../deployments/test-results", "report output directory")
	flag.Parse()

	if bootstrapUser == "" || bootstrapPass == "" {
		fmt.Fprintln(os.Stderr, "bootstrap-manager and bootstrap-password are required")
		os.Exit(2)
	}

	t := &tester{
		runID:       runID,
		baseURL:     strings.TrimRight(baseURL, "/"),
		frontendURL: strings.TrimRight(frontendURL, "/"),
	}
	t.run(bootstrapUser, bootstrapPass)

	jsonPath, markdownPath, err := t.writeReports(reportDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write reports: %v\n", err)
		os.Exit(2)
	}

	passed, failed := t.counts()
	fmt.Printf("RUNTIME_API_TEST run=%s passed=%d failed=%d total=%d\n", runID, passed, failed, len(t.results))
	fmt.Printf("JSON_REPORT=%s\nMARKDOWN_REPORT=%s\n", jsonPath, markdownPath)
	for _, r := range t.results {
		state := "PASS"
		if !r.Passed {
			state = "FAIL"
		}
		fmt.Printf("%s | %s | expected=%s | actual=%s", state, r.Name, r.Expected, r.Actual)
		if r.Evidence != "" {
			fmt.Printf(" | %s", r.Evidence)
		}
		fmt.Println()
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func newSession(base string) *session {
	jar, _ := cookiejar.New(nil)
	return &session{
		base: strings.TrimRight(base, "/"),
		jar:  jar,
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
		},
	}
}

func (s *session) do(method, path string, payload any, csrfCookie string, includeCSRF bool) (response, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return response{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.base+path, body)
	if err != nil {
		return response{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if includeCSRF {
		if token := s.cookieValue(path, csrfCookie); token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: raw, header: resp.Header.Clone()}, err
}

func (s *session) cookieValue(path, name string) string {
	u, _ := url.Parse(s.base + path)
	for _, c := range s.jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (s *session) cookieHeader(path string, names ...string) (string, string) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	u, _ := url.Parse(s.base + path)
	var parts []string
	csrf := ""
	for _, c := range s.jar.Cookies(u) {
		if wanted[c.Name] {
			parts = append(parts, c.Name+"="+c.Value)
			if strings.HasSuffix(c.Name, "_csrf") {
				csrf = c.Value
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; "), csrf
}

func manualRequest(base, method, path, cookieHeader, csrf, authorization string, payload any) (response, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return response{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return response{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: raw, header: resp.Header.Clone()}, err
}

func (t *tester) add(name, endpoint, expected, actual string, passed bool, evidence string) {
	t.results = append(t.results, result{
		Name: name, Endpoint: endpoint, Expected: expected, Actual: actual, Passed: passed, Evidence: evidence,
	})
}

func (t *tester) httpResult(name, endpoint string, expectedStatus int, r response, extra bool, evidence string) {
	actual := fmt.Sprintf("HTTP %d", r.status)
	t.add(name, endpoint, fmt.Sprintf("HTTP %d", expectedStatus), actual, r.status == expectedStatus && extra, evidence)
}

func (t *tester) requestResult(name, endpoint string, expectedStatus int, r response, err error, extra bool, evidence string) {
	if err != nil {
		t.add(name, endpoint, fmt.Sprintf("HTTP %d", expectedStatus), "request error", false, err.Error())
		return
	}
	t.httpResult(name, endpoint, expectedStatus, r, extra, evidence)
}

func jsonMap(raw []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func nested(v any, keys ...string) any {
	cur := v
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asID(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		return n.String()
	case string:
		return n
	default:
		return ""
	}
}

func arrayContainsString(v any, wanted string) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if asString(item) == wanted {
			return true
		}
	}
	return false
}

func listContainsDocument(v any, wanted string) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if asString(nested(item, "documentId")) == wanted {
			return true
		}
	}
	return false
}

func listContainsTitle(v any, wanted string) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if asString(nested(item, "title")) == wanted {
			return true
		}
	}
	return false
}

func listContainsManagerRequest(v any, username string) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if asString(nested(item, "username")) == username {
			return true
		}
	}
	return false
}

func bodyCode(r response) string {
	decoder := json.NewDecoder(bytes.NewReader(r.body))
	var first map[string]any
	if err := decoder.Decode(&first); err != nil {
		return "INVALID_JSON"
	}
	code := asString(nested(first, "error", "code"))
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return code + "+TRAILING_JSON"
	}
	return code
}

func (t *tester) run(bootstrapUser, bootstrapPass string) {
	t.runEntryPoints()
	t.runCORSAndRouteSurface()

	password := "E2ePass!234"
	emailA := "e2e." + t.runID + ".a@example.test"
	emailB := "e2e." + t.runID + ".b@example.test"
	emailC := "e2e." + t.runID + ".proxy@example.test"
	managerApprove := "e2e_" + t.runID + "_approve"
	managerReject := "e2e_" + t.runID + "_reject"

	public := newSession(t.baseURL)
	unauth := newSession(t.baseURL)
	r, err := unauth.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("未登录访问保护接口", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	r, err = public.do(http.MethodPost, "/api/v1/public/auth/register", map[string]any{"email": "bad", "password": "123", "nickname": "x"}, "", false)
	t.requestResult("用户注册参数校验", "POST /api/v1/public/auth/register", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	r, err = public.do(http.MethodPost, "/api/v1/public/auth/register", map[string]any{"email": emailA, "password": password, "nickname": "E2E-A"}, "", false)
	userAID := asID(nested(jsonMap(r.body), "data", "id"))
	cleanUserA := !bytes.Contains(r.body, []byte("password"))
	t.requestResult("注册用户 A", "POST /api/v1/public/auth/register", 201, r, err, userAID != "" && cleanUserA, "userId="+userAID+", passwordFieldAbsent="+strconv.FormatBool(cleanUserA))

	r, err = public.do(http.MethodPost, "/api/v1/public/auth/register", map[string]any{"email": emailB, "password": password, "nickname": "E2E-B"}, "", false)
	userBID := asID(nested(jsonMap(r.body), "data", "id"))
	t.requestResult("注册用户 B", "POST /api/v1/public/auth/register", 201, r, err, userBID != "", "userId="+userBID)

	r, err = public.do(http.MethodPost, "/api/v1/public/auth/register", map[string]any{"email": emailA, "password": password, "nickname": "E2E-A"}, "", false)
	t.requestResult("重复邮箱注册冲突", "POST /api/v1/public/auth/register", 409, r, err, bodyCode(r) == "CONFLICT", "code="+bodyCode(r))

	r, err = public.do(http.MethodPost, "/api/v1/public/auth/login", map[string]any{"email": emailA, "password": "wrong-password"}, "", false)
	t.requestResult("错误密码登录", "POST /api/v1/public/auth/login", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	userAOld := newSession(t.baseURL)
	r, err = userAOld.do(http.MethodPost, "/api/v1/public/auth/login", map[string]any{"email": emailA, "password": password}, "", false)
	cookieEvidence, cookieOK := inspectCookieHeaders(r.header.Values("Set-Cookie"), "pp_user")
	t.requestResult("用户 Cookie-only 登录与属性", "POST /api/v1/public/auth/login", 200, r, err, cookieOK, cookieEvidence)

	accessToken := userAOld.cookieValue("/api/v1/protected/documents/mine", "pp_user_at")
	r, err = manualRequest(t.baseURL, http.MethodGet, "/api/v1/protected/documents/mine", "", "", "Bearer "+accessToken, nil)
	t.requestResult("Bearer 凭据不替代 Cookie", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "Authorization ignored; code="+bodyCode(r))

	csrfMissingTitle := "csrf-missing-" + t.runID
	csrfInvalidTitle := "csrf-invalid-" + t.runID
	r, err = userAOld.do(http.MethodPost, "/api/v1/protected/documents", map[string]any{"title": csrfMissingTitle, "content": "csrf"}, "pp_user_csrf", false)
	t.requestResult("写接口缺少 CSRF", "POST /api/v1/protected/documents", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))
	r, err = manualRequest(t.baseURL, http.MethodPost, "/api/v1/protected/documents", userAOldCookie(userAOld, "/api/v1/protected/documents"), "invalid-csrf", "", map[string]any{"title": csrfInvalidTitle, "content": "csrf"})
	t.requestResult("写接口错误 CSRF", "POST /api/v1/protected/documents", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))
	r, err = userAOld.do(http.MethodGet, "/api/v1/protected/documents/mine?page=1&pageSize=100", nil, "", false)
	csrfList := nested(jsonMap(r.body), "data", "documentList")
	csrfBlocked := !listContainsTitle(csrfList, csrfMissingTitle) && !listContainsTitle(csrfList, csrfInvalidTitle)
	t.requestResult("CSRF 拒绝不产生写入副作用", "GET /api/v1/protected/documents/mine", 200, r, err, csrfBlocked, "missingPresent="+strconv.FormatBool(listContainsTitle(csrfList, csrfMissingTitle))+", invalidPresent="+strconv.FormatBool(listContainsTitle(csrfList, csrfInvalidTitle)))

	oldRefreshCookies, oldRefreshCSRF := userAOld.cookieHeader("/api/v1/public/auth/refresh", "pp_user_rt", "pp_user_csrf")
	userA := newSession(t.baseURL)
	r, err = userA.do(http.MethodPost, "/api/v1/public/auth/login", map[string]any{"email": emailA, "password": password}, "", false)
	t.requestResult("同用户新登录建立新 sid", "POST /api/v1/public/auth/login", 200, r, err, true, "new session created")

	r, err = userAOld.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("新登录使旧 Access 失效", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
	r, err = manualRequest(t.baseURL, http.MethodPost, "/api/v1/public/auth/refresh", oldRefreshCookies, oldRefreshCSRF, "", nil)
	t.requestResult("新登录使旧 Refresh 失效", "POST /api/v1/public/auth/refresh", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	rotatedCookies, rotatedCSRF := userA.cookieHeader("/api/v1/public/auth/refresh", "pp_user_rt", "pp_user_csrf")
	r, err = userA.do(http.MethodPost, "/api/v1/public/auth/refresh", nil, "pp_user_csrf", true)
	t.requestResult("用户 Refresh 轮换", "POST /api/v1/public/auth/refresh", 200, r, err, true, "token pair rotated")
	r, err = manualRequest(t.baseURL, http.MethodPost, "/api/v1/public/auth/refresh", rotatedCookies, rotatedCSRF, "", nil)
	t.requestResult("用户旧 Refresh 防重放", "POST /api/v1/public/auth/refresh", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
	r, err = userA.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("Refresh 后 Access 可用", "GET /api/v1/protected/documents/mine", 200, r, err, true, "current sid remains active")

	userB := newSession(t.baseURL)
	r, err = userB.do(http.MethodPost, "/api/v1/public/auth/login", map[string]any{"email": emailB, "password": password}, "", false)
	t.requestResult("用户 B 登录", "POST /api/v1/public/auth/login", 200, r, err, true, "cookie session established")

	publicDocumentID, privateDocumentID := t.runDocuments(userA, userB)
	_ = publicDocumentID
	_ = privateDocumentID
	t.runManager(userA, bootstrapUser, bootstrapPass, managerApprove, managerReject, password)
	t.runProxy(emailC, password)
	t.runLogout(userA, userB)
}

func (t *tester) runEntryPoints() {
	for _, tc := range []struct {
		name, base, path, marker string
	}{
		{"后端存活检查", t.baseURL, "/healthz", `"status":"ok"`},
		{"后端就绪检查", t.baseURL, "/readyz", `"status":"ready"`},
		{"Nginx 代理存活检查", t.frontendURL, "/healthz", `"status":"ok"`},
		{"Nginx 代理就绪检查", t.frontendURL, "/readyz", `"status":"ready"`},
		{"前端静态入口", t.frontendURL, "/", `<div id="app">`},
		{"前端 History fallback", t.frontendURL, "/app/library", `<div id="app">`},
	} {
		s := newSession(tc.base)
		r, err := s.do(http.MethodGet, tc.path, nil, "", false)
		t.requestResult(tc.name, "GET "+tc.path, 200, r, err, bytes.Contains(r.body, []byte(tc.marker)), "marker="+tc.marker)
	}
}

func (t *tester) runCORSAndRouteSurface() {
	request := func(origin string) (response, error) {
		req, _ := http.NewRequest(http.MethodOptions, t.baseURL+"/api/v1/public/auth/login", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return response{}, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return response{status: resp.StatusCode, body: raw, header: resp.Header.Clone()}, nil
	}
	r, err := request("http://localhost:15173")
	allowed := r.header.Get("Access-Control-Allow-Origin") == "http://localhost:15173" && r.header.Get("Access-Control-Allow-Credentials") == "true"
	t.requestResult("允许来源 CORS 预检", "OPTIONS /api/v1/public/auth/login", 204, r, err, allowed, "allow-origin="+r.header.Get("Access-Control-Allow-Origin"))
	r, err = request("https://invalid.example")
	t.requestResult("拒绝未配置 CORS 来源", "OPTIONS /api/v1/public/auth/login", 403, r, err, r.header.Get("Access-Control-Allow-Origin") == "", "allow-origin absent")

	s := newSession(t.baseURL)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/protected/chat/ws"},
		{http.MethodGet, "/api/v1/protected/chat/sse"},
		{http.MethodPost, "/api/v1/protected/chat/messages"},
		{http.MethodPost, "/api/v1/protected/chat/sse/messages"},
		{http.MethodPost, "/api/v1/protected/chat/deliveries/00000000-0000-0000-0000-000000000000/ack"},
	} {
		r, err := s.do(tc.method, tc.path, map[string]any{}, "", false)
		t.requestResult("已停用的 Chat 路由不可达", tc.method+" "+tc.path, 404, r, err, r.status == http.StatusNotFound, "code="+bodyCode(r))
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/protected/markdown/mine"},
		{http.MethodPost, "/api/v1/protected/markdown/upload"},
		{http.MethodGet, "/api/v1/protected/markdown/00000000-0000-0000-0000-000000000000"},
	} {
		r, err := s.do(tc.method, tc.path, map[string]any{}, "", false)
		t.requestResult("旧 Markdown 路由不可达", tc.method+" "+tc.path, 404, r, err, r.status == http.StatusNotFound, "code="+bodyCode(r))
	}
}

func (t *tester) runDocuments(a, b *session) (string, string) {
	keyword := "orion" + strings.ReplaceAll(t.runID, "_", "")
	r, err := a.do(http.MethodPost, "/api/v1/protected/documents", map[string]any{"title": "bad", "content": "bad", "visibility": "friends"}, "pp_user_csrf", true)
	t.requestResult("Markdown 可见性校验", "POST /api/v1/protected/documents", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	r, err = a.do(http.MethodPost, "/api/v1/protected/documents", map[string]any{"title": keyword + " public", "content": "# " + keyword + "\npublic body", "visibility": "public"}, "pp_user_csrf", true)
	publicID := asString(nested(jsonMap(r.body), "data", "documentId"))
	t.requestResult("创建公开 Markdown", "POST /api/v1/protected/documents", 201, r, err, publicID != "", "documentId="+publicID)

	r, err = a.do(http.MethodPost, "/api/v1/protected/documents", map[string]any{"title": keyword + " private", "content": "# " + keyword + "\nprivate body", "visibility": "private"}, "pp_user_csrf", true)
	privateID := asString(nested(jsonMap(r.body), "data", "documentId"))
	t.requestResult("创建私有 Markdown", "POST /api/v1/protected/documents", 201, r, err, privateID != "", "documentId="+privateID)

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/mine?page=1&pageSize=10", nil, "", false)
	mine := nested(jsonMap(r.body), "data", "documentList")
	metaOK := nested(jsonMap(r.body), "meta", "page") != nil && nested(jsonMap(r.body), "meta", "per_page") != nil
	t.requestResult("我的 Markdown 分页", "GET /api/v1/protected/documents/mine", 200, r, err, listContainsDocument(mine, publicID) && listContainsDocument(mine, privateID) && metaOK, "contains public/private and meta")

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/public?page=1&pageSize=100", nil, "", false)
	publicList := nested(jsonMap(r.body), "data", "documentList")
	t.requestResult("公开 Markdown 列表过滤", "GET /api/v1/protected/documents/public", 200, r, err, listContainsDocument(publicList, publicID) && !listContainsDocument(publicList, privateID), "public visible; private absent")

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/search?keyword="+url.QueryEscape(keyword)+"&page=1&pageSize=10", nil, "", false)
	searchList := nested(jsonMap(r.body), "data", "documentList")
	t.requestResult("Markdown BM25 全文搜索", "GET /api/v1/protected/documents/search", 200, r, err, listContainsDocument(searchList, publicID) && listContainsDocument(searchList, privateID), "unique keyword matched both owned documents")

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/search?keyword=%20%20", nil, "", false)
	t.requestResult("Markdown 空搜索词校验", "GET /api/v1/protected/documents/search", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/search?keyword="+url.QueryEscape(strings.Repeat("文", 101)), nil, "", false)
	t.requestResult("Markdown 超长搜索词校验", "GET /api/v1/protected/documents/search", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/mine?page=0&pageSize=101", nil, "", false)
	t.requestResult("Markdown 分页边界校验", "GET /api/v1/protected/documents/mine", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/"+publicID, nil, "", false)
	t.requestResult("作者读取公开 Markdown", "GET /api/v1/protected/documents/:id", 200, r, err, asString(nested(jsonMap(r.body), "data", "content")) == "# "+keyword+"\npublic body", "full content matched")
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/"+privateID, nil, "", false)
	t.requestResult("作者读取私有 Markdown", "GET /api/v1/protected/documents/:id", 200, r, err, asString(nested(jsonMap(r.body), "data", "visibility")) == "private", "visibility=private")
	r, err = b.do(http.MethodGet, "/api/v1/protected/documents/"+publicID, nil, "", false)
	t.requestResult("其他用户读取公开 Markdown", "GET /api/v1/protected/documents/:id", 200, r, err, r.status == http.StatusOK, "public access granted")
	r, err = b.do(http.MethodGet, "/api/v1/protected/documents/"+privateID, nil, "", false)
	t.requestResult("其他用户禁止读取私有 Markdown", "GET /api/v1/protected/documents/:id", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))
	r, err = b.do(http.MethodGet, "/api/v1/protected/documents/00000000-0000-0000-0000-000000000000", nil, "", false)
	t.requestResult("不存在 Markdown", "GET /api/v1/protected/documents/:id", 404, r, err, bodyCode(r) == "NOT_FOUND", "code="+bodyCode(r))

	r, err = a.do(http.MethodPut, "/api/v1/protected/documents/"+publicID, map[string]any{"title": "  ", "content": "invalid update", "visibility": "public"}, "pp_user_csrf", true)
	t.requestResult("Markdown 更新参数校验", "PUT /api/v1/protected/documents/:id", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	updatedKeyword := "updated" + strings.ReplaceAll(t.runID, "_", "")
	updatedBody := "# " + updatedKeyword + "\nupdated body"
	updatePayload := map[string]any{"title": updatedKeyword + " private", "content": updatedBody, "visibility": "private"}
	r, err = b.do(http.MethodPut, "/api/v1/protected/documents/"+publicID, updatePayload, "pp_user_csrf", true)
	t.requestResult("其他用户禁止编辑 Markdown", "PUT /api/v1/protected/documents/:id", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))

	r, err = a.do(http.MethodPut, "/api/v1/protected/documents/"+publicID, updatePayload, "pp_user_csrf", true)
	updateOK := asString(nested(jsonMap(r.body), "data", "content")) == updatedBody &&
		asString(nested(jsonMap(r.body), "data", "visibility")) == "private"
	t.requestResult("作者编辑 Markdown", "PUT /api/v1/protected/documents/:id", 200, r, err, updateOK, "content and visibility updated")

	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/"+publicID, nil, "", false)
	cacheUpdated := asString(nested(jsonMap(r.body), "data", "content")) == updatedBody
	t.requestResult("Markdown 更新后缓存失效", "GET /api/v1/protected/documents/:id", 200, r, err, cacheUpdated, "updated content returned")
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/search?keyword="+url.QueryEscape(updatedKeyword)+"&page=1&pageSize=10", nil, "", false)
	updatedSearchList := nested(jsonMap(r.body), "data", "documentList")
	t.requestResult("Markdown 更新后搜索索引生效", "GET /api/v1/protected/documents/search", 200, r, err, listContainsDocument(updatedSearchList, publicID), "updated keyword matched document")
	r, err = b.do(http.MethodGet, "/api/v1/protected/documents/"+publicID, nil, "", false)
	t.requestResult("更新为私有后其他用户不可读", "GET /api/v1/protected/documents/:id", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))

	r, err = b.do(http.MethodDelete, "/api/v1/protected/documents/"+publicID, nil, "pp_user_csrf", true)
	t.requestResult("其他用户禁止删除 Markdown", "DELETE /api/v1/protected/documents/:id", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))
	r, err = a.do(http.MethodDelete, "/api/v1/protected/documents/"+publicID, nil, "pp_user_csrf", true)
	t.requestResult("作者删除 Markdown", "DELETE /api/v1/protected/documents/:id", 200, r, err, asString(nested(jsonMap(r.body), "data", "documentId")) == publicID, "documentId="+publicID)
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/"+publicID, nil, "", false)
	t.requestResult("删除后 Markdown 不可读取", "GET /api/v1/protected/documents/:id", 404, r, err, bodyCode(r) == "NOT_FOUND", "code="+bodyCode(r))
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/search?keyword="+url.QueryEscape(updatedKeyword)+"&page=1&pageSize=10", nil, "", false)
	deletedSearchList := nested(jsonMap(r.body), "data", "documentList")
	t.requestResult("Markdown 删除后搜索结果移除", "GET /api/v1/protected/documents/search", 200, r, err, !listContainsDocument(deletedSearchList, publicID), "deleted document absent")
	r, err = a.do(http.MethodDelete, "/api/v1/protected/documents/"+publicID, nil, "pp_user_csrf", true)
	t.requestResult("重复删除 Markdown", "DELETE /api/v1/protected/documents/:id", 404, r, err, bodyCode(r) == "NOT_FOUND", "code="+bodyCode(r))
	return publicID, privateID
}

func (t *tester) runManager(user *session, bootstrapUser, bootstrapPass, approveUser, rejectUser, password string) {
	public := newSession(t.baseURL)
	csrfProbeUser := approveUser + "_csrf"
	r, err := public.do(http.MethodPost, "/api/v1/public/manager/register", map[string]any{"username": "x", "password": "1"}, "", false)
	t.requestResult("管理员申请参数校验", "POST /api/v1/public/manager/register", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	r, err = public.do(http.MethodPost, "/api/v1/public/manager/register", map[string]any{"username": approveUser, "password": password, "email": approveUser + "@example.test", "reason": "runtime e2e approve"}, "", false)
	approveID := asID(nested(jsonMap(r.body), "data", "requestId"))
	t.requestResult("提交待通过管理员申请", "POST /api/v1/public/manager/register", 201, r, err, approveID != "", "requestId="+approveID)
	r, err = public.do(http.MethodPost, "/api/v1/public/manager/register", map[string]any{"username": rejectUser, "password": password, "email": rejectUser + "@example.test", "reason": "runtime e2e reject"}, "", false)
	rejectID := asID(nested(jsonMap(r.body), "data", "requestId"))
	t.requestResult("提交待拒绝管理员申请", "POST /api/v1/public/manager/register", 201, r, err, rejectID != "", "requestId="+rejectID)
	r, err = public.do(http.MethodPost, "/api/v1/public/manager/register", map[string]any{"username": csrfProbeUser, "password": password, "email": csrfProbeUser + "@example.test", "reason": "runtime e2e csrf probe"}, "", false)
	csrfProbeID := asID(nested(jsonMap(r.body), "data", "requestId"))
	t.requestResult("提交 CSRF 副作用探针申请", "POST /api/v1/public/manager/register", 201, r, err, csrfProbeID != "", "requestId="+csrfProbeID)
	r, err = public.do(http.MethodPost, "/api/v1/public/manager/register", map[string]any{"username": approveUser, "password": password}, "", false)
	t.requestResult("重复管理员申请冲突", "POST /api/v1/public/manager/register", 409, r, err, bodyCode(r) == "CONFLICT", "code="+bodyCode(r))
	r, err = public.do(http.MethodPost, "/api/v1/public/manager/login", map[string]any{"username": approveUser, "password": password}, "", false)
	t.requestResult("待审批管理员禁止登录", "POST /api/v1/public/manager/login", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	admin := newSession(t.baseURL)
	r, err = admin.do(http.MethodPost, "/api/v1/public/manager/login", map[string]any{"username": bootstrapUser, "password": bootstrapPass}, "", false)
	cookieEvidence, cookieOK := inspectCookieHeaders(r.header.Values("Set-Cookie"), "pp_manager")
	t.requestResult("引导管理员登录与 Cookie 属性", "POST /api/v1/public/manager/login", 200, r, err, cookieOK, cookieEvidence)

	r, err = user.do(http.MethodGet, "/api/v1/protected/manager/requests?status=pending", nil, "", false)
	t.requestResult("用户 Cookie 不能访问管理端", "GET /api/v1/protected/manager/requests", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "scope isolated")
	r, err = admin.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("管理员 Cookie 不能访问用户端", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "scope isolated")

	r, err = admin.do(http.MethodGet, "/api/v1/protected/manager/requests?status=pending&page=1&pageSize=100", nil, "", false)
	requests := nested(jsonMap(r.body), "data", "requests")
	noHash := !bytes.Contains(r.body, []byte("password_hash")) && !bytes.Contains(r.body, []byte("passwordHash")) && !bytes.Contains(r.body, []byte(`"password"`))
	t.requestResult("管理员申请列表与敏感字段隐藏", "GET /api/v1/protected/manager/requests", 200, r, err, listContainsManagerRequest(requests, approveUser) && listContainsManagerRequest(requests, rejectUser) && noHash, "both requests present; password hash absent")
	r, err = admin.do(http.MethodGet, "/api/v1/protected/manager/requests?page=0&pageSize=101", nil, "", false)
	t.requestResult("管理员列表分页边界", "GET /api/v1/protected/manager/requests", 400, r, err, bodyCode(r) == "VALIDATION_FAILED", "code="+bodyCode(r))

	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/"+csrfProbeID+"/approve", map[string]any{"comment": "must-not-approve"}, "pp_manager_csrf", false)
	t.requestResult("审批缺少管理员 CSRF", "POST /api/v1/protected/manager/requests/:id/approve", 403, r, err, bodyCode(r) == "FORBIDDEN", "code="+bodyCode(r))
	r, err = admin.do(http.MethodGet, "/api/v1/protected/manager/requests?status=pending&page=1&pageSize=100", nil, "", false)
	csrfStillPending := listContainsManagerRequest(nested(jsonMap(r.body), "data", "requests"), csrfProbeUser)
	t.requestResult("CSRF 拒绝不执行管理员审批", "GET /api/v1/protected/manager/requests", 200, r, err, csrfStillPending, "probeStillPending="+strconv.FormatBool(csrfStillPending))
	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/"+approveID+"/approve", map[string]any{"comment": "approved by runtime test"}, "pp_manager_csrf", true)
	t.requestResult("通过管理员申请", "POST /api/v1/protected/manager/requests/:id/approve", 200, r, err, asString(nested(jsonMap(r.body), "data", "status")) == "active", "status=active")
	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/"+approveID+"/approve", map[string]any{}, "pp_manager_csrf", true)
	t.requestResult("重复审批冲突", "POST /api/v1/protected/manager/requests/:id/approve", 409, r, err, bodyCode(r) == "CONFLICT", "code="+bodyCode(r))
	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/"+rejectID+"/reject", map[string]any{"comment": "rejected by runtime test"}, "pp_manager_csrf", true)
	t.requestResult("拒绝管理员申请", "POST /api/v1/protected/manager/requests/:id/reject", 200, r, err, asString(nested(jsonMap(r.body), "data", "status")) == "rejected", "status=rejected")
	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/"+rejectID+"/reject", map[string]any{}, "pp_manager_csrf", true)
	t.requestResult("重复拒绝冲突", "POST /api/v1/protected/manager/requests/:id/reject", 409, r, err, bodyCode(r) == "CONFLICT", "code="+bodyCode(r))
	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/requests/999999999/approve", map[string]any{}, "pp_manager_csrf", true)
	t.requestResult("审批不存在申请", "POST /api/v1/protected/manager/requests/:id/approve", 404, r, err, bodyCode(r) == "NOT_FOUND", "code="+bodyCode(r))

	for _, status := range []string{"approved", "rejected"} {
		r, err = admin.do(http.MethodGet, "/api/v1/protected/manager/requests?status="+status+"&page=1&pageSize=100", nil, "", false)
		wanted := approveUser
		if status == "rejected" {
			wanted = rejectUser
		}
		t.requestResult("按状态查询 "+status+" 申请", "GET /api/v1/protected/manager/requests", 200, r, err, listContainsManagerRequest(nested(jsonMap(r.body), "data", "requests"), wanted), "status filter matched")
	}

	approved := newSession(t.baseURL)
	r, err = approved.do(http.MethodPost, "/api/v1/public/manager/login", map[string]any{"username": approveUser, "password": password}, "", false)
	t.requestResult("审批后管理员可登录", "POST /api/v1/public/manager/login", 200, r, err, true, "active account authenticated")
	oldCookies, oldCSRF := approved.cookieHeader("/api/v1/public/manager/refresh", "pp_manager_rt", "pp_manager_csrf")
	r, err = approved.do(http.MethodPost, "/api/v1/public/manager/refresh", nil, "pp_manager_csrf", true)
	t.requestResult("管理员 Refresh 轮换", "POST /api/v1/public/manager/refresh", 200, r, err, true, "token pair rotated")
	r, err = manualRequest(t.baseURL, http.MethodPost, "/api/v1/public/manager/refresh", oldCookies, oldCSRF, "", nil)
	t.requestResult("管理员旧 Refresh 防重放", "POST /api/v1/public/manager/refresh", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
	r, err = approved.do(http.MethodPost, "/api/v1/protected/manager/logout", nil, "pp_manager_csrf", true)
	t.requestResult("审批管理员登出", "POST /api/v1/protected/manager/logout", 200, r, err, true, "session revoked")
	r, err = approved.do(http.MethodGet, "/api/v1/protected/manager/requests", nil, "", false)
	t.requestResult("审批管理员登出后失效", "GET /api/v1/protected/manager/requests", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	r, err = admin.do(http.MethodPost, "/api/v1/protected/manager/logout", nil, "pp_manager_csrf", true)
	t.requestResult("引导管理员登出", "POST /api/v1/protected/manager/logout", 200, r, err, true, "session revoked")
	r, err = admin.do(http.MethodGet, "/api/v1/protected/manager/requests", nil, "", false)
	t.requestResult("引导管理员登出后失效", "GET /api/v1/protected/manager/requests", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
}

func (t *tester) runProxy(email, password string) {
	s := newSession(t.frontendURL)
	r, err := s.do(http.MethodPost, "/api/v1/public/auth/register", map[string]any{"email": email, "password": password, "nickname": "E2E-Proxy"}, "", false)
	t.requestResult("Nginx 代理用户注册", "POST /api/v1/public/auth/register via :15173", 201, r, err, asID(nested(jsonMap(r.body), "data", "id")) != "", "API reverse proxy")
	r, err = s.do(http.MethodPost, "/api/v1/public/auth/login", map[string]any{"email": email, "password": password}, "", false)
	t.requestResult("Nginx 代理 Cookie 登录", "POST /api/v1/public/auth/login via :15173", 200, r, err, true, "same-origin cookie session")
	r, err = s.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("Nginx 代理保护接口", "GET /api/v1/protected/documents/mine via :15173", 200, r, err, true, "authenticated proxy request")
	r, err = s.do(http.MethodPost, "/api/v1/protected/auth/logout", nil, "pp_user_csrf", true)
	t.requestResult("Nginx 代理用户登出", "POST /api/v1/protected/auth/logout via :15173", 200, r, err, true, "session revoked")
}

func (t *tester) runLogout(a, b *session) {
	bOldCookies, bOldCSRF := b.cookieHeader("/api/v1/public/auth/refresh", "pp_user_rt", "pp_user_csrf")
	r, err := b.do(http.MethodPost, "/api/v1/protected/auth/logout", nil, "pp_user_csrf", true)
	t.requestResult("用户 B 登出", "POST /api/v1/protected/auth/logout", 200, r, err, true, "cookie cleared; session revoked")
	r, err = b.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("登出后 Access 失效", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
	r, err = manualRequest(t.baseURL, http.MethodPost, "/api/v1/public/auth/refresh", bOldCookies, bOldCSRF, "", nil)
	t.requestResult("登出后旧 Refresh 失效", "POST /api/v1/public/auth/refresh", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))

	r, err = a.do(http.MethodPost, "/api/v1/protected/auth/logout", nil, "pp_user_csrf", true)
	t.requestResult("用户 A 登出", "POST /api/v1/protected/auth/logout", 200, r, err, true, "session revoked")
	r, err = a.do(http.MethodGet, "/api/v1/protected/documents/mine", nil, "", false)
	t.requestResult("用户 A 登出后失效", "GET /api/v1/protected/documents/mine", 401, r, err, bodyCode(r) == "UNAUTHORIZED", "code="+bodyCode(r))
}

func userAOldCookie(s *session, path string) string {
	u, _ := url.Parse(s.base + path)
	var parts []string
	for _, c := range s.jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func inspectCookieHeaders(headers []string, prefix string) (string, bool) {
	joined := strings.Join(headers, "\n")
	access := strings.Contains(joined, prefix+"_at=") && strings.Contains(joined, prefix+"_at=")
	refresh := strings.Contains(joined, prefix+"_rt=")
	csrf := strings.Contains(joined, prefix+"_csrf=")
	accessPath := strings.Contains(joined, prefix+"_at=") && strings.Contains(joined, "Path=/api/v1/protected")
	refreshPath := strings.Contains(joined, prefix+"_rt=") && strings.Contains(joined, "Path=/api/v1/public/")
	httpOnlyCount := strings.Count(joined, "HttpOnly") >= 2
	sameSite := strings.Count(joined, "SameSite=Lax") >= 3
	ok := access && refresh && csrf && accessPath && refreshPath && httpOnlyCount && sameSite
	return fmt.Sprintf("access=%t refresh=%t csrf=%t scopedPaths=%t httpOnlyTokens=%t sameSiteLax=%t", access, refresh, csrf, accessPath && refreshPath, httpOnlyCount, sameSite), ok
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func boolState(ok bool) string {
	if ok {
		return "observed"
	}
	return "not observed"
}

func (t *tester) counts() (int, int) {
	passed := 0
	for _, r := range t.results {
		if r.Passed {
			passed++
		}
	}
	return passed, len(t.results) - passed
}

func (t *tester) writeReports(reportDir string) (string, string, error) {
	abs, err := filepath.Abs(reportDir)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", "", err
	}
	jsonPath := filepath.Join(abs, "full-api-"+t.runID+".json")
	markdownPath := filepath.Join(abs, "full-api-"+t.runID+".md")

	jsonPayload := struct {
		RunID       string    `json:"runId"`
		GeneratedAt time.Time `json:"generatedAt"`
		BaseURL     string    `json:"baseUrl"`
		FrontendURL string    `json:"frontendUrl"`
		Passed      int       `json:"passed"`
		Failed      int       `json:"failed"`
		Results     []result  `json:"results"`
	}{RunID: t.runID, GeneratedAt: time.Now(), BaseURL: t.baseURL, FrontendURL: t.frontendURL, Results: t.results}
	jsonPayload.Passed, jsonPayload.Failed = t.counts()
	raw, _ := json.MarshalIndent(jsonPayload, "", "  ")
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return "", "", err
	}

	var md strings.Builder
	fmt.Fprintf(&md, "# 部署实例全量接口测试报告\n\n")
	fmt.Fprintf(&md, "- Run ID: `%s`\n- 生成时间: `%s`\n- Backend: `%s`\n- Frontend/Nginx: `%s`\n- 结果: **%d passed / %d failed / %d total**\n\n", t.runID, time.Now().Format(time.RFC3339), t.baseURL, t.frontendURL, jsonPayload.Passed, jsonPayload.Failed, len(t.results))
	md.WriteString("| 状态 | 用例 | 接口 | 预期 | 实际 | 证据 |\n|---|---|---|---|---|---|\n")
	for _, r := range t.results {
		state := "PASS"
		if !r.Passed {
			state = "FAIL"
		}
		fmt.Fprintf(&md, "| %s | %s | `%s` | %s | %s | %s |\n", state, mdCell(r.Name), mdCell(r.Endpoint), mdCell(r.Expected), mdCell(r.Actual), mdCell(r.Evidence))
	}
	if err := os.WriteFile(markdownPath, []byte(md.String()), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, markdownPath, nil
}

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
