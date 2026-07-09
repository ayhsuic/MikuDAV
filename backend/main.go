package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("缺少环境变量 DATABASE_URL，请设置后重试")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}
	defer pool.Close()
	fmt.Println("PostgreSQL 连接成功")

	if err := initDB(pool); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("缺少环境变量 PORT，请设置后重试")
	}
	fmt.Printf("后端服务启动于 http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
