param(
    [switch]$AutoStart,
    [switch]$Background
)

$ErrorActionPreference = "Stop"

$InstallDir = Join-Path $env:LOCALAPPDATA "DiskPulse"
$ExePath = Join-Path $InstallDir "diskpulse.exe"
$ReleaseUrl = "https://github.com/donamgit151524/diskpulse/releases/latest/download/diskpulse.exe"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Stop running instance if upgrading
Get-Process -Name "diskpulse" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 400

$LocalExe = if ($PSScriptRoot) { Join-Path $PSScriptRoot "diskpulse.exe" } else { "" }
if ($LocalExe -and (Test-Path $LocalExe)) {
    Write-Host "Copying local diskpulse.exe to $ExePath..."
    Copy-Item -Force $LocalExe $ExePath
} else {
    Write-Host "Downloading latest DiskPulse from GitHub Releases..."
    Invoke-WebRequest -Uri $ReleaseUrl -OutFile $ExePath
}

# Create Start Menu shortcut
$StartMenuDir = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$ShortcutPath = Join-Path $StartMenuDir "DiskPulse.lnk"
$WshShell = New-Object -ComObject WScript.Shell
$Shortcut = $WshShell.CreateShortcut($ShortcutPath)
$Shortcut.TargetPath = $ExePath
$Shortcut.WorkingDirectory = $InstallDir
$Shortcut.Description = "DiskPulse - Disk Growth Tracker"
$Shortcut.Save()

if ($AutoStart) {
    $RunKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    Set-ItemProperty -Path $RunKey -Name "DiskPulse" -Value "`"$ExePath`" --background"
    Write-Host "Enabled startup with Windows."
}

$ArgList = @()
if ($Background) {
    $ArgList += "--background"
}

Write-Host "Starting DiskPulse..."
if ($ArgList.Count -gt 0) {
    Start-Process -FilePath $ExePath -ArgumentList $ArgList
} else {
    Start-Process -FilePath $ExePath
}

Write-Host "Done! DiskPulse is installed at $ExePath"
