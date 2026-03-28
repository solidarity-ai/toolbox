package tool

import "testing"

func TestFQNParseModulePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ModulePath
		wantErr bool
	}{
		{name: "valid basic", input: "github.com/solidarity-ai", want: ModulePath("github.com/solidarity-ai")},
		{name: "valid nested", input: "example.com/acme/toolbox", want: ModulePath("example.com/acme/toolbox")},
		{name: "empty", input: "", wantErr: true},
		{name: "single segment", input: "github.com", wantErr: true},
		{name: "host missing dot", input: "github/acme", wantErr: true},
		{name: "empty segment", input: "github.com//acme", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseModulePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseModulePath(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModulePath(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseModulePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
			parsedAgain, err := ParseModulePath(got.String())
			if err != nil {
				t.Fatalf("ParseModulePath(%q) round-trip error = %v", got.String(), err)
			}
			if parsedAgain != got {
				t.Fatalf("ParseModulePath(%q) round-trip = %q, want %q", got.String(), parsedAgain, got)
			}
		})
	}
}

func TestFQNParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		want       Version
		wantErr    bool
		wantPseudo bool
		wantTS     string
		wantCommit string
	}{
		{name: "valid semver", input: "v1.2.3", want: Version("v1.2.3")},
		{name: "valid semver prerelease", input: "v1.2.3-alpha.1", want: Version("v1.2.3-alpha.1")},
		{name: "valid semver build", input: "v1.2.3+build.5", want: Version("v1.2.3+build.5")},
		{name: "valid semver prerelease build", input: "v1.2.3-rc.1+build.5", want: Version("v1.2.3-rc.1+build.5")},
		{name: "valid pseudo", input: "v0.0.0-20260327112233-abcdef123456", want: Version("v0.0.0-20260327112233-abcdef123456"), wantPseudo: true, wantTS: "20260327112233", wantCommit: "abcdef123456"},
		{name: "empty", input: "", wantErr: true},
		{name: "missing leading v", input: "1.2.3", wantErr: true},
		{name: "missing patch", input: "v1.2", wantErr: true},
		{name: "bad pseudo timestamp", input: "v0.0.0-20260327-abcdef123456", wantErr: true},
		{name: "bad pseudo commit length", input: "v0.0.0-20260327112233-abcdef12345", wantErr: true},
		{name: "bad pseudo commit charset", input: "v0.0.0-20260327112233-abcdeg123456", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
			parsedAgain, err := ParseVersion(got.String())
			if err != nil {
				t.Fatalf("ParseVersion(%q) round-trip error = %v", got.String(), err)
			}
			if parsedAgain != got {
				t.Fatalf("ParseVersion(%q) round-trip = %q, want %q", got.String(), parsedAgain, got)
			}
			if got.IsPseudo() != tt.wantPseudo {
				t.Fatalf("Version(%q).IsPseudo() = %v, want %v", got, got.IsPseudo(), tt.wantPseudo)
			}
			if got.PseudoTimestamp() != tt.wantTS {
				t.Fatalf("Version(%q).PseudoTimestamp() = %q, want %q", got, got.PseudoTimestamp(), tt.wantTS)
			}
			if got.PseudoCommit() != tt.wantCommit {
				t.Fatalf("Version(%q).PseudoCommit() = %q, want %q", got, got.PseudoCommit(), tt.wantCommit)
			}
		})
	}
}

func TestFQNParseToolPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ToolPath
		wantErr bool
	}{
		{name: "valid single segment", input: "echo", want: ToolPath("echo")},
		{name: "valid nested", input: "git.hub.release", want: ToolPath("git.hub.release")},
		{name: "empty", input: "", wantErr: true},
		{name: "leading dot", input: ".echo", wantErr: true},
		{name: "trailing dot", input: "echo.", wantErr: true},
		{name: "double dot", input: "git..release", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseToolPath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseToolPath(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToolPath(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseToolPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
			parsedAgain, err := ParseToolPath(got.String())
			if err != nil {
				t.Fatalf("ParseToolPath(%q) round-trip error = %v", got.String(), err)
			}
			if parsedAgain != got {
				t.Fatalf("ParseToolPath(%q) round-trip = %q, want %q", got.String(), parsedAgain, got)
			}
		})
	}
}

func TestFQNParsePackageVer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    PackageVer
		wantErr bool
	}{
		{
			name:  "valid semver",
			input: "github.com/solidarity-ai/toolbox@v1.2.3",
			want:  PackageVer{Module: ModulePath("github.com/solidarity-ai/toolbox"), Version: Version("v1.2.3")},
		},
		{
			name:  "valid pseudo",
			input: "github.com/solidarity-ai/toolbox@v0.0.0-20260327112233-abcdef123456",
			want:  PackageVer{Module: ModulePath("github.com/solidarity-ai/toolbox"), Version: Version("v0.0.0-20260327112233-abcdef123456")},
		},
		{name: "missing at", input: "github.com/solidarity-ai/toolbox", wantErr: true},
		{name: "missing version", input: "github.com/solidarity-ai/toolbox@", wantErr: true},
		{name: "invalid module", input: "github/toolbox@v1.2.3", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePackageVer(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePackageVer(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePackageVer(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePackageVer(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
			parsedAgain, err := ParsePackageVer(got.String())
			if err != nil {
				t.Fatalf("ParsePackageVer(%q) round-trip error = %v", got.String(), err)
			}
			if parsedAgain != got {
				t.Fatalf("ParsePackageVer(%q) round-trip = %#v, want %#v", got.String(), parsedAgain, got)
			}
		})
	}
}

func TestFQNParseToolFQN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ToolFQN
		wantErr bool
	}{
		{
			name:  "valid semver",
			input: "github.com/solidarity-ai/toolbox@v1.2.3/git.hub.release",
			want: ToolFQN{
				Module:  ModulePath("github.com/solidarity-ai/toolbox"),
				Version: Version("v1.2.3"),
				Tool:    ToolPath("git.hub.release"),
			},
		},
		{
			name:  "valid pseudo",
			input: "github.com/solidarity-ai/toolbox@v0.0.0-20260327112233-abcdef123456/git.hub.release",
			want: ToolFQN{
				Module:  ModulePath("github.com/solidarity-ai/toolbox"),
				Version: Version("v0.0.0-20260327112233-abcdef123456"),
				Tool:    ToolPath("git.hub.release"),
			},
		},
		{name: "missing at", input: "github.com/solidarity-ai/toolbox/v1.2.3/git.hub.release", wantErr: true},
		{name: "missing slash", input: "github.com/solidarity-ai/toolbox@v1.2.3", wantErr: true},
		{name: "missing tool path", input: "github.com/solidarity-ai/toolbox@v1.2.3/", wantErr: true},
		{name: "invalid version", input: "github.com/solidarity-ai/toolbox@1.2.3/git.hub.release", wantErr: true},
		{name: "invalid module", input: "github/toolbox@v1.2.3/git.hub.release", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseToolFQN(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseToolFQN(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToolFQN(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseToolFQN(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
			parsedAgain, err := ParseToolFQN(got.String())
			if err != nil {
				t.Fatalf("ParseToolFQN(%q) round-trip error = %v", got.String(), err)
			}
			if parsedAgain != got {
				t.Fatalf("ParseToolFQN(%q) round-trip = %#v, want %#v", got.String(), parsedAgain, got)
			}
		})
	}
}
