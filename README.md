# DiskPulse — Công cụ theo dõi biến động dung lượng ổ cứng (Windows)

DiskPulse chạy ngầm siêu nhẹ ở khay hệ thống (System Tray), tự động chụp snapshot cây thư mục định kỳ vào SQLite (mặc định **100 phút/lần**) và cảnh báo thông minh khi ổ sắp đầy hoặc có thư mục tăng dung lượng bất thường.

## Tính năng chính

- **Disk Growth Tracking (Time Machine):** So sánh giữa 2 mốc thời gian (`24h qua`, `7 ngày qua`, `30 ngày qua`, hoặc chọn 2 snapshot bất kỳ). Thuật toán **Root-Cause Attribution** lọc trùng thư mục cha để chỉ thẳng nhánh gốc đang phình to (ví dụ `Docker\wsl`, `node_modules`, `Downloads`, `Temp`, cache AI...).
- **Cảnh báo thông minh & nhẹ nhàng:**
  - Ổ đĩa sắp đầy (theo `%` hoặc `GB`).
  - Tăng đột biến trong 24h (`Sudden Spike`).
  - Rò rỉ âm thầm nhiều lần liên tiếp (`Creeping Growth`).
  - Dự báo số ngày còn lại trước khi đầy ổ (`Linear Regression Forecast`).
  - Chống spam bằng cơ chế cooldown; hỗ trợ Windows Tray Toast và tùy chọn Discord Webhook / Telegram Bot.
- **An toàn trên Windows NTFS:** Bỏ qua Reparse Points / Junctions / Symlinks để tránh lặp vô hạn và đếm trùng; bỏ qua file cloud-only của OneDrive để không kích hoạt tải file từ cloud; tính đúng dung lượng thực trên đĩa cho sparse file (như `ext4.vhdx`).
- **Tự dọn DB cũ (Retention):** Giữ toàn bộ snapshot 14 ngày gần nhất, 1 snapshot/ngày cho 15–90 ngày, và 1 snapshot/tuần sau 90 ngày.

---

## Cài đặt khi reset máy hoặc trên máy mới

### Cách 1: Cài nhanh 1 lệnh (không cần cài Go)

Mở **PowerShell** và chạy lệnh sau để tải bản `diskpulse.exe` mới nhất từ GitHub Releases, tạo shortcut trong Start Menu và bật tự khởi động cùng Windows:

```powershell
irm https://raw.githubusercontent.com/donamgit151524/diskpulse/main/install.ps1 | iex
```

Hoặc tải thủ công file `diskpulse.exe` từ mục **Releases** trên GitHub về một thư mục cố định (ví dụ `C:\Tools\DiskPulse\diskpulse.exe`) rồi chạy trực tiếp. Trong giao diện web hoặc chuột phải icon ở System Tray, chọn **Khởi động cùng Windows**.

> **Mẹo khi sắp reset máy:** Nếu muốn giữ lại toàn bộ lịch sử snapshot và cấu hình cũ, hãy sao lưu thư mục `%LocalAppData%\DiskPulse\` (`diskpulse.db` và `config.json`) sang USB/OneDrive trước khi reset, rồi chép lại vào đúng vị trí đó trên máy mới.

### Cách 2: Tự build từ mã nguồn (cần Go 1.24+)

1. Cài Git và Go (`winget install Git.Git GoLang.Go`).
2. Clone repo và build:

```powershell
git clone https://github.com/donamgit151524/diskpulse.git
cd diskpulse

# Chạy unit tests
go test ./...

# Build file thực thi Windows (ẩn cửa sổ console khi chạy nền)
go build -ldflags="-s -w -H=windowsgui" -o diskpulse.exe ./cmd/diskpulse

# Cài vào %LocalAppData%\DiskPulse và bật khởi động cùng Windows
.\install.ps1 -AutoStart
```

---

## Cách sử dụng

- Mở `diskpulse.exe`: ứng dụng nằm ở **System Tray** (góc dưới phải màn hình) và mở Dashboard tại `http://127.0.0.1:47821`.
- Dùng cờ `--background` (`diskpulse.exe --background`) để chạy ngầm mà không tự bật trình duyệt.
- Dữ liệu cấu hình và database SQLite được lưu tại `%LocalAppData%\DiskPulse\`.

---

## Sao lưu dữ liệu (Google Drive hoặc Git)

### Cách khuyên dùng: Sao lưu 1 chạm lên Google Drive

Trên Dashboard, bấm nút **Sao lưu dữ liệu (.zip)** ở thanh bên trái (hoặc trong tab **Cài đặt**). App sẽ dùng `VACUUM INTO` của SQLite để đóng gói một file `diskpulse-backup-YYYYMMDD-HHMMSS.zip` sạch (gồm `diskpulse.db` và `config.json`) **ngay khi đang chạy mà không cần tắt app**.

- Kéo thả file `.zip` này lên Google Drive (hoặc lưu vào thư mục Google Drive Desktop).
- Khi reset máy / sang máy mới: giải nén file `.zip` vào `%LocalAppData%\DiskPulse\` trước khi mở `diskpulse.exe`.

### Hoặc sao lưu lên Git (Private Repo)

> **Lưu ý bảo mật:** Không đẩy `diskpulse.db` hoặc `config.json` lên repo **public**, vì `diskpulse.db` chứa đường dẫn thư mục/file trên máy bạn và `config.json` có thể chứa Webhook/Telegram token. Hãy tạo một repo **private** riêng để lưu dữ liệu.

### 1. Đẩy dữ liệu hiện tại lên GitHub (lần đầu)

```powershell
# Tắt DiskPulse để SQLite ghi trọn vẹn dữ liệu từ file -wal vào diskpulse.db
Get-Process -Name "diskpulse" -ErrorAction SilentlyContinue | Stop-Process -Force

cd "$env:LOCALAPPDATA\DiskPulse"
git init -b main
git add diskpulse.db config.json
git commit -m "Backup DiskPulse data"
gh repo create diskpulse-backup --private --source=. --remote=origin --push
```

### 2. Cập nhật bản sao lưu định kỳ

```powershell
Get-Process -Name "diskpulse" -ErrorAction SilentlyContinue | Stop-Process -Force
cd "$env:LOCALAPPDATA\DiskPulse"
git add diskpulse.db config.json
git commit -m "Backup $(Get-Date -Format 'yyyy-MM-dd HH:mm')"
git push
```

### 3. Khôi phục dữ liệu khi reset máy hoặc sang máy mới

Chạy trước khi mở DiskPulse (hoặc tắt DiskPulse trước khi chép đè):

```powershell
gh repo clone diskpulse-backup "$env:LOCALAPPDATA\DiskPulse"
```

Sau đó cài/mở lại `diskpulse.exe`, toàn bộ lịch sử snapshot và cấu hình sẽ được giữ nguyên.
