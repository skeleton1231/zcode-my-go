package webfetch

import (
	"net/url"
	"regexp"
	"strings"
)

// preapprovedHosts 是文档类只读站点白名单（ZCode 同款清单——命中即只读
// 自动放行，权限询问留给其余主机）。语言/框架官方文档站是 agent 的高
// 频只读目标，逐次询问的摩擦大于风险。
var preapprovedHosts = map[string]bool{
	"modelcontextprotocol.io": true, "agentskills.io": true,
	"docs.python.org": true, "en.cppreference.com": true,
	"docs.oracle.com": true, "learn.microsoft.com": true,
	"developer.mozilla.org": true, "go.dev": true, "pkg.go.dev": true,
	"www.php.net": true, "docs.swift.org": true, "kotlinlang.org": true,
	"ruby-doc.org": true, "doc.rust-lang.org": true,
	"www.typescriptlang.org": true, "react.dev": true, "angular.io": true,
	"vuejs.org": true, "nextjs.org": true, "expressjs.com": true,
	"nodejs.org": true, "bun.sh": true, "jquery.com": true,
	"getbootstrap.com": true, "tailwindcss.com": true, "d3js.org": true,
	"threejs.org": true, "redux.js.org": true, "webpack.js.org": true,
	"jestjs.io": true, "reactrouter.com": true,
	"docs.djangoproject.com": true, "flask.palletsprojects.com": true,
	"fastapi.tiangolo.com": true, "pandas.pydata.org": true,
	"numpy.org": true, "www.tensorflow.org": true, "pytorch.org": true,
	"scikit-learn.org": true, "matplotlib.org": true,
	"requests.readthedocs.io": true, "jupyter.org": true,
	"laravel.com": true, "symfony.com": true, "docs.spring.io": true,
	"hibernate.org": true, "tomcat.apache.org": true, "gradle.org": true,
	"maven.apache.org": true, "asp.net": true, "dotnet.microsoft.com": true,
	"blazor.net": true, "reactnative.dev": true, "docs.flutter.dev": true,
	"developer.apple.com": true, "developer.android.com": true,
	"keras.io": true, "spark.apache.org": true, "www.mongodb.com": true,
	"redis.io": true, "www.postgresql.org": true, "dev.mysql.com": true,
	"www.sqlite.org": true, "graphql.org": true, "prisma.io": true,
	"docs.getdbt.com": true, "docs.aws.amazon.com": true,
	"cloud.google.com": true, "kubernetes.io": true, "www.docker.com": true,
	"www.terraform.io": true, "www.ansible.com": true, "docs.stripe.com": true,
	"docs.netlify.com": true, "devcenter.heroku.com": true,
	"cypress.io": true, "selenium.dev": true, "docs.unity.com": true,
	"docs.unrealengine.com": true, "git-scm.com": true,
	"nginx.org": true, "httpd.apache.org": true,
}

// preapprovedPathPrefixes 是「仅限指定路径前缀」的白名单（站点其余部
// 分仍需询问）。
var preapprovedPathPrefixes = map[string][]string{
	"wordpress.org":  {"/documentation"},
	"huggingface.co": {"/docs"},
	"www.kaggle.com": {"/docs"},
	"vercel.com":     {"/docs"},
}

// reEncodedSlash 点捕获路径里的编码斜杠/点（%2f/%5c/%2e 及双重编码）——
// 用编码形态绕过路径前缀判定的通道。
var reEncodedSlash = regexp.MustCompile(`(?i)%(25)*(2f|5c|2e)`)

// IsPreapprovedURL 报告 URL 是否落在预批准面（只读自动放行）。解析失
// 败即 false（保守）。
func IsPreapprovedURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := NormalizeHost(u.Hostname())
	if preapprovedHosts[host] {
		return true
	}
	prefixes, ok := preapprovedPathPrefixes[host]
	if !ok {
		return false
	}
	if reEncodedSlash.MatchString(u.EscapedPath()) {
		return false
	}
	path := u.Path
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
