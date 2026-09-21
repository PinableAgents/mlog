package main

import (
	"time"

	"github.com/PinableAgents/mlog"
	"go.uber.org/zap"
)

func exampleSingleFileMode() {
	println("\n=== 示例1：单文件模式 ===")

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
		SingleFile:     true,
		SingleFileName: "all.log",
	}

	mlog.InitialZap("single_file_service", 1001, "debug", config)

	mlog.Debug("这是一条 Debug 日志 - 调试信息")
	mlog.Info("这是一条 Info 日志 - 应用启动成功")
	mlog.Warn("这是一条 Warn 日志 - 配置项缺失，使用默认值")
	mlog.Error("这是一条 Error 日志 - 连接数据库失败")

	println("所有日志都写入到: ./example_logs/single_file/1001/single_file_service/all.log")

	mlog.Close()
}

func exampleCustomFileName() {
	println("\n=== 示例2：自定义文件名的单文件模式 ===")

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
		SingleFile:     true,
		SingleFileName: "application.log",
	}

	mlog.InitialZap("custom_app", 2001, "info", config)

	mlog.Info("应用启动 %s %s %s %d", "version", "1.0.0", "port", 8080)
	mlog.Warn("配置项缺失 %s %s %s %s", "key", "database.host", "default", "localhost")
	mlog.Error("连接失败 %s %s %s %s", "service", "redis", "error", "connection timeout")

	println("所有日志都写入到: ./example_logs/custom_name/2001/custom_app/application.log")

	mlog.Close()
}

func exampleMultiFileMode() {
	println("\n=== 示例3：多文件模式（默认） ===")

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
		SingleFile:     false,
	}

	mlog.InitialZap("multi_file_service", 3001, "debug", config)

	mlog.Debug("这是一条 Debug 日志 - 详细的调试信息")
	mlog.Info("这是一条 Info 日志 - 正常的业务信息")
	mlog.Warn("这是一条 Warn 日志 - 警告信息")
	mlog.Error("这是一条 Error 日志 - 错误信息")

	println("日志分别写入到:")
	println("  - ./example_logs/multi_file/3001/multi_file_service/debug.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/info.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/warn.log")
	println("  - ./example_logs/multi_file/3001/multi_file_service/error.log")

	mlog.Close()
}

func exampleStructuredLogging() {
	println("\n=== 示例4：结构化日志（单文件模式） ===")

	config := &mlog.ZapConfig{
		Level:          "info",
		Format:         "json",
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

	mlog.InitialZap("structured_service", 4001, "info", config)

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

	mlog.Close()
}

func exampleBusinessDirectory() {
	println("\n=== 示例5：业务目录分类（单文件模式） ===")

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

	mlog.InitialZap("business_service", 5001, "info", config)

	mlog.InfoW("订单创建成功",
		zap.String("business", "order"),
		zap.String("order_id", "ORD-2024-001"),
		zap.Float64("amount", 99.99),
	)

	mlog.InfoW("支付完成",
		zap.String("business", "payment"),
		zap.String("payment_id", "PAY-2024-001"),
		zap.String("method", "alipay"),
	)

	mlog.InfoW("用户注册",
		zap.String("folder", "user"),
		zap.Int("user_id", 10001),
		zap.String("email", "user@example.com"),
	)

	println("单文件模式：business/folder 作为元数据，全部写入:")
	println("  - ./example_logs/business/5001/business_service/all.log")

	mlog.Close()
}

func main() {
	println("==============================================")
	println("mlog 单文件模式使用示例")
	println("==============================================")

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
