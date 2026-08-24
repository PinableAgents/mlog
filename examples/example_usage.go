package main

import (
	"time"

	"github.com/PinableAgents/mlog"
	"go.uber.org/zap"
)

// 示例1：使用单文件模式（所有日志写入一个文件）
func exampleSingleFileMode() {
	println("\n=== 示例1：单文件模式 ===")

	// 配置单文件模式
	config := &mlog.ZapConfig{
		Level:          "debug",
		Format:         "console",
		Director:       "./example_logs/single_file",
		EncodeLevel:    "CapitalColorLevelEncoder",
		StacktraceKey:  "stacktrace",
		ShowLine:       true,
		LogInConsole:   true,
		RetentionDay:   30,
		MaxSize:        100,
		MaxBackups:     5,
		EnableCompress: false,
		SingleFile:     true,      // 启用单文件模式
		SingleFileName: "all.log", // 所有日志写入 all.log
	}

	// 初始化日志系统
	mlog.InitialZap("single_file_service", 1001, "debug", config)

	// 记录不同级别的日志，都会写入到同一个文件
	mlog.Debug("这是一条 Debug 日志 - 调试信息")
	mlog.Info("这是一条 Info 日志 - 应用启动成功")
	mlog.Warn("这是一条 Warn 日志 - 配置项缺失，使用默认值")
	mlog.Error("这是一条 Error 日志 - 连接数据库失败")

	println("所有日志都写入到: ./example_logs/single_file/1001/single_file_service/all.log")

	// 等待日志写入完成
	time.Sleep(100 * time.Millisecond)
}

// 示例2：使用自定义文件名的单文件模式
func exampleCustomFileName() {
	println("\n=== 示例2：自定义文件名的单文件模式 ===")

	// 配置单文件模式，使用自定义文件名
	config := &mlog.ZapConfig{
		Level:          "info",
		Format:         "json",
		Director:       "./example_logs/custom_name",
		EncodeLevel:    "LowercaseLevelEncoder",
		StacktraceKey:  "stacktrace",
		ShowLine:       true,
		LogInConsole:   true,
		RetentionDay:   7,
		MaxSize:        50,
		MaxBackups:     3,
		EnableCompress: false,
		SingleFile:     true,              // 启用单文件模式
		SingleFileName: "application.log", // 自定义文件名
	}

	// 初始化日志系统
	mlog.InitialZap("custom_app", 2001, "info", config)

	// 记录日志
	mlog.Info("应用启动 %s %s %s %d", "version", "1.0.0", "port", 8080)
	mlog.Warn("配置项缺失 %s %s %s %s", "key", "database.host", "default", "localhost")
	mlog.Error("连接失败 %s %s %s %s", "service", "redis", "error", "connection timeout")

	println("所有日志都写入到: ./example_logs/custom_name/2001/custom_app/application.log")

	// 等待日志写入完成
	time.Sleep(100 * time.Millisecond)
}

// 示例3：使用多文件模式（默认模式，按级别分文件）
func exampleMultiFileMode() {
	println("\n=== 示例3：多文件模式（默认） ===")

	// 配置多文件模式（默认）
	config := &mlog.ZapConfig{
		Level:          "debug",
		Format:         "console",
		Director:       "./example_logs/multi_file",
		EncodeLevel:    "CapitalColorLevelEncoder",
		StacktraceKey:  "stacktrace",
		ShowLine:       true,
		LogInConsole:   true,
		RetentionDay:   30,
		MaxSize:        100,
		MaxBackups:     5,
		EnableCompress: false,
		SingleFile:     false, // 禁用单文件模式（默认）
	}

	// 初始化日志系统
	mlog.InitialZap("multi_file_service", 3001, "debug", config)

	// 记录不同级别的日志，会写入到不同的文件
	mlog.Debug("这是一条 Debug 日志 - 详细的调试信息")
	mlog.Info("这是一条 Info 日志 - 正常的业务信息")
	mlog.Warn("这是一条 Warn 日志 - 警告信息")
	mlog.Error("这是一条 Error 日志 - 错误信息")

	println("日志分别写入到:")
	println("  - ./example_logs/multi_file/3001/multi_file_service/debug.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/info.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/warn.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/error.log")

	// 等待日志写入完成
	time.Sleep(100 * time.Millisecond)
}

// 示例4：使用带字段的结构化日志（单文件模式）
func exampleStructuredLogging() {
	println("\n=== 示例4：结构化日志（单文件模式） ===")

	// 配置单文件模式
	config := &mlog.ZapConfig{
		Level:          "info",
		Format:         "json", // 使用 JSON 格式
		Director:       "./example_logs/structured",
		EncodeLevel:    "LowercaseLevelEncoder",
		StacktraceKey:  "stacktrace",
		ShowLine:       true,
		LogInConsole:   true,
		RetentionDay:   30,
		MaxSize:        100,
		MaxBackups:     5,
		EnableCompress: false,
		SingleFile:     true,
		SingleFileName: "structured.log",
	}

	// 初始化日志系统
	mlog.InitialZap("structured_service", 4001, "info", config)

	// 使用结构化日志记录
	mlog.InfoW("用户登录",
		zap.Int("user_id", 12345),
		zap.String("username", "john_doe"),
		zap.String("ip", "192.168.1.100"),
		zap.Int64("timestamp", time.Now().Unix()),
	)

	mlog.WarnW("API 调用超时",
		zap.String("api", "/api/v1/users"),
		zap.Int("duration_ms", 5000),
		zap.Int("threshold_ms", 3000),
	)

	mlog.ErrorW("数据库查询失败",
		zap.String("query", "SELECT * FROM users WHERE id = ?"),
		zap.String("error", "connection timeout"),
		zap.Int("retry_count", 3),
	)

	println("结构化日志写入到: ./example_logs/structured/4001/structured_service/structured.log")

	// 等待日志写入完成
	time.Sleep(100 * time.Millisecond)
}

// 示例5：使用业务目录分类（单文件模式）
func exampleBusinessDirectory() {
	println("\n=== 示例5：业务目录分类（单文件模式） ===")

	// 配置单文件模式
	config := &mlog.ZapConfig{
		Level:          "info",
		Format:         "console",
		Director:       "./example_logs/business",
		EncodeLevel:    "CapitalColorLevelEncoder",
		StacktraceKey:  "stacktrace",
		ShowLine:       true,
		LogInConsole:   true,
		RetentionDay:   30,
		MaxSize:        100,
		MaxBackups:     5,
		EnableCompress: false,
		SingleFile:     true,
		SingleFileName: "all.log",
	}

	// 初始化日志系统
	mlog.InitialZap("business_service", 5001, "info", config)

	// 记录到不同的业务目录
	mlog.InfoW("订单创建成功",
		zap.String("business", "order"), // 会创建 order 子目录
		zap.String("order_id", "ORD-2024-001"),
		zap.Float64("amount", 99.99),
	)

	mlog.InfoW("支付完成",
		zap.String("business", "payment"), // 会创建 payment 子目录
		zap.String("payment_id", "PAY-2024-001"),
		zap.String("method", "alipay"),
	)

	mlog.InfoW("用户注册",
		zap.String("folder", "user"), // 会创建 user 子目录
		zap.Int("user_id", 10001),
		zap.String("email", "user@example.com"),
	)

	println("业务日志分别写入到:")
	println("  - ./example_logs/business/5001/business_service/order/all.log")
	println("  - ./example_logs/business/5001/business_service/payment/all.log")
	println("  - ./example_logs/business/5001/business_service/user/all.log")

	// 等待日志写入完成
	time.Sleep(100 * time.Millisecond)
}

func main() {
	println("==============================================")
	println("mlog 单文件模式使用示例")
	println("==============================================")

	// 运行各个示例
	exampleSingleFileMode()
	exampleCustomFileName()
	exampleMultiFileMode()
	exampleStructuredLogging()
	exampleBusinessDirectory()

	println("\n==============================================")
	println("所有示例执行完成！")
	println("请查看 ./example_logs 目录下的日志文件")
	println("==============================================")
}
