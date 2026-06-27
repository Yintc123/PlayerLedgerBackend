# PlayerLedger Backend

玩家儲值紀錄查詢工具的 API 伺服器，使用 Golang 建置。

## 技術棧

- [Go](https://golang.org/)
- [AWS Lambda](https://aws.amazon.com/lambda/) — 無伺服器執行環境
- [AWS API Gateway](https://aws.amazon.com/api-gateway/) — HTTP 路由與入口

## 快速開始

### 安裝相依套件

```bash
go mod download
```

### 啟動開發伺服器

```bash
go run main.go
```

API 預設監聽於 `http://localhost:8080`。

### 建置與部署至 AWS Lambda

建置 Lambda 相容的執行檔（需使用 Linux/amd64）：

```bash
GOOS=linux GOARCH=amd64 go build -o bootstrap main.go
zip deployment.zip bootstrap
```

部署至 Lambda：

```bash
aws lambda update-function-code \
  --function-name playerledger-api \
  --zip-file fileb://deployment.zip
```

## 專案結構

```
PlayerLedgerBackend/
├── main.go         # 程式進入點
├── handler/        # HTTP 處理器
├── service/        # 業務邏輯
├── repository/     # 資料存取層
└── model/          # 資料結構定義
```

## API 端點

| 方法 | 路徑 | 說明 |
|------|------|------|
| GET | `/api/players/:id/transactions` | 查詢玩家儲值紀錄 |
