package tagging

import "strings"

const (
	TagDocker         = "docker"
	TagAIModels       = "ai-models"
	TagDevDeps        = "dev-deps"
	TagPkgCache       = "pkg-cache"
	TagTempLogs       = "temp-logs"
	TagDownloadsMedia = "downloads-media"
	TagVCS            = "vcs"
	TagWindowsSystem  = "windows-system"
	TagOther          = "other"
)

type rule struct {
	tag      string
	patterns []string
}

var pathRules = []rule{
	{
		tag: TagDocker,
		patterns: []string{
			`\docker\wsl`,
			`\docker`,
			`ext4.vhdx`,
			`docker_data.vhdx`,
			`\wsl`,
			`.vhdx`,
		},
	},
	{
		tag: TagAIModels,
		patterns: []string{
			`\.cache\huggingface`,
			`\ollama\models`,
			`\.ollama\models`,
			`\.cache\torch`,
			`\.cache\whisper`,
			`\lm-studio\models`,
			`\stable-diffusion`,
			`\comfyui\models`,
		},
	},
	{
		tag: TagDevDeps,
		patterns: []string{
			`\node_modules`,
			`\.venv`,
			`\venv`,
			`\.gradle`,
			`\.nuget`,
			`\__pycache__`,
			`\target\debug`,
			`\target\release`,
		},
	},
	{
		tag: TagPkgCache,
		patterns: []string{
			`\npm-cache`,
			`\pnpm-store`,
			`\pnpm\store`,
			`\pip\cache`,
			`\go\pkg\mod`,
			`\.cargo\registry`,
			`\yarn\cache`,
			`\uv\cache`,
			`\appdata\local\pip`,
			`\appdata\local\npm-cache`,
		},
	},
	{
		tag: TagTempLogs,
		patterns: []string{
			`\appdata\local\temp`,
			`\windows\temp`,
			`\crashdumps`,
			`\minidump`,
			`\logs`,
			`.log`,
			`.tmp`,
			`.dmp`,
		},
	},
	{
		tag: TagDownloadsMedia,
		patterns: []string{
			`\downloads`,
			`\videos`,
			`\movies`,
		},
	},
	{
		tag: TagVCS,
		patterns: []string{
			`\.git`,
		},
	},
	{
		tag: TagWindowsSystem,
		patterns: []string{
			`\windows\winsxs`,
			`\windows\softwaredistribution`,
			`\windows\installer`,
			`\$recycle.bin`,
			`hiberfil.sys`,
			`pagefile.sys`,
			`swapfile.sys`,
		},
	},
}

// ClassifyPath assigns a semantic category tag based on normalized lowercase Windows path patterns.
func ClassifyPath(path string) string {
	lower := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	for _, r := range pathRules {
		for _, p := range r.patterns {
			if strings.HasPrefix(p, ".") && !strings.Contains(p, `\`) {
				if strings.HasSuffix(lower, p) {
					return r.tag
				}
				continue
			}
			if strings.Contains(lower, p) {
				// Avoid matching substrings like \downloads_backup unless segment boundary matches.
				if strings.HasPrefix(p, `\`) && !strings.HasSuffix(p, `\`) {
					idx := strings.Index(lower, p)
					for idx >= 0 {
						end := idx + len(p)
						if end == len(lower) || lower[end] == '\\' {
							return r.tag
						}
						next := strings.Index(lower[idx+1:], p)
						if next < 0 {
							break
						}
						idx = idx + 1 + next
					}
					continue
				}
				return r.tag
			}
		}
	}
	return TagOther
}

// HumanLabel returns a Vietnamese display label for a category tag.
func HumanLabel(tag string) string {
	switch tag {
	case TagDocker:
		return "Docker / WSL"
	case TagAIModels:
		return "Model AI / Cache ML"
	case TagDevDeps:
		return "Thư viện dự án (Dev)"
	case TagPkgCache:
		return "Cache trình quản lý gói"
	case TagTempLogs:
		return "File tạm & Log"
	case TagDownloadsMedia:
		return "Tải về & Media"
	case TagVCS:
		return "Lịch sử Git"
	case TagWindowsSystem:
		return "Hệ thống Windows"
	default:
		return "Khác"
	}
}

// CleanupHint returns a short safety hint explaining what the path usually is.
func CleanupHint(tag string) string {
	switch tag {
	case TagDocker:
		return "Ảnh đĩa WSL/Docker — kiểm tra `docker system df` hoặc compact VHDX trước khi xóa."
	case TagAIModels:
		return "Trọng số model AI — có thể tải lại nhưng dung lượng lớn, kiểm tra model nào không còn dùng."
	case TagDevDeps:
		return "Phụ thuộc dự án (node_modules, .venv...) — thường có thể xóa ở dự án cũ và cài lại khi cần."
	case TagPkgCache:
		return "Bộ nhớ đệm package — an toàn để dọn bằng lệnh cache clean của công cụ tương ứng."
	case TagTempLogs:
		return "File tạm hoặc log — kiểm tra file log đang phình to bất thường trước khi dọn."
	case TagDownloadsMedia:
		return "File người dùng tải về hoặc media — kiểm tra thủ công trước khi xóa."
	case TagVCS:
		return "Lịch sử Git — cân nhắc `git gc` hoặc kiểm tra file nhị phân lỡ commit."
	case TagWindowsSystem:
		return "Thành phần Windows/Thùng rác — nên dọn bằng Disk Cleanup hoặc Storage Sense."
	default:
		return "Kiểm tra nội dung thư mục trong File Explorer trước khi xử lý."
	}
}
