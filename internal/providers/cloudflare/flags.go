package cloudflare

import (
	"flag"
	"strings"
)

type cloudflareFlagValues struct {
	APIURL  *string
	Token   *string
	Workdir *string
}

func RegisterCloudflareProviderFlags(fs *flag.FlagSet, defaults Config) any {
	return cloudflareFlagValues{
		APIURL:  fs.String("cloudflare-url", defaults.Cloudflare.APIURL, "Cloudflare runner API URL"),
		Token:   fs.String("cloudflare-token", "", "Cloudflare runner bearer token"),
		Workdir: fs.String("cloudflare-workdir", defaults.Cloudflare.Workdir, "Absolute working directory inside the Cloudflare workspace"),
	}
}

func ApplyCloudflareProviderFlags(cfg *Config, fs *flag.FlagSet, values any) error {
	if isCloudflareProviderName(cfg.Provider) {
		instanceType := strings.TrimSpace(cfg.ServerType)
		if instanceType == "" {
			instanceType = cloudflareContainerInstanceTypeForClass(cfg.Class)
		}
		normalized, ok := normalizeCloudflareContainerInstanceType(instanceType)
		if !ok {
			return exit(2, "%s --type must be one of %s", providerName, strings.Join(cloudflareContainerInstanceTypes(), ", "))
		}
		cfg.ServerType = normalized
		cfg.ServerTypeExplicit = flagWasSet(fs, "type") || cfg.ServerTypeExplicit
	}
	v, ok := values.(cloudflareFlagValues)
	if !ok {
		return nil
	}
	if flagWasSet(fs, "cloudflare-url") {
		cfg.Cloudflare.APIURL = *v.APIURL
	}
	if flagWasSet(fs, "cloudflare-token") {
		cfg.Cloudflare.Token = *v.Token
	}
	if flagWasSet(fs, "cloudflare-workdir") {
		cfg.Cloudflare.Workdir = *v.Workdir
	}
	return nil
}

func isCloudflareProviderName(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case providerName, providerAlias:
		return true
	default:
		return false
	}
}
