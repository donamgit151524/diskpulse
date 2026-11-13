package tagging

import "testing"

func TestClassifyPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{`C:\Users\dovan\AppData\Local\Docker\wsl\disk\docker_data.vhdx`, TagDocker},
		{`C:\Users\dovan\Docker`, TagDocker},
		{`C:\Users\dovan\project\node_modules`, TagDevDeps},
		{`C:\Users\dovan\project\node_modules_backup`, TagOther},
		{`C:\Users\dovan\.cache\huggingface\hub`, TagAIModels},
		{`C:\Users\dovan\AppData\Local\pip\cache`, TagPkgCache},
		{`C:\Users\dovan\AppData\Local\Temp\build.log`, TagTempLogs},
		{`C:\Users\dovan\Downloads\ubuntu.iso`, TagDownloadsMedia},
		{`C:\Users\dovan\repo\.git`, TagVCS},
		{`C:\$Recycle.Bin\S-1-5-21`, TagWindowsSystem},
		{`C:\Games\SomeGame`, TagOther},
	}

	for _, tc := range tests {
		got := ClassifyPath(tc.path)
		if got != tc.want {
			t.Errorf("ClassifyPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
