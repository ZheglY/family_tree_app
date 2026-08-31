package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

/*
Создаем ключ. Ключ нужен, чтобы сохранить и затем получить
logger из context.Context.

Ключ применяется:
context.WithValue(ctx, key, log)
— для сохранения logger в контекст;
ctx.Value(key)
— для получения logger из контекста.
*/
type loggerContextKey struct{}

var (
	key = loggerContextKey{} // создаёт уникальный тип ключа, который не конфликтует с ключами других пакетов.
)

type Logger struct {
	*zap.Logger
	file *os.File
}

// NewNop returns a logger suitable for tests and small isolated components.
func NewNop() *Logger {
	return &Logger{Logger: zap.NewNop()}
}

// Функция создаёт logger, который одновременно пишет
// сообщения в консоль и в файл.
func NewLogger(config LoggerConfig) (*Logger, error) {
	// Создаётся уровень логирования Zap. Слово Atomic означает,
	// что значение уровня можно безопасно менять во время работы
	// приложения из разных горутин.
	zapLvl := zap.NewAtomicLevel()

	// Метод UnmarshalText преобразует env LOGGER_LEVEL во внутренний тип Zap.
	// Метод работает с байтами, поэтому предварительно переводим
	if err := zapLvl.UnmarshalText([]byte(config.Level)); err != nil {
		return nil, fmt.Errorf("failed to unmarshal level: %w", err)
	}

	// Создаёт папку для логов. 0755 — права доступа в Unix-системах
	if err := os.MkdirAll(config.Folder, 0755); err != nil {
		return nil, fmt.Errorf("failed to make dir log folder: %w", err)
	}

	// Берется текущее время и становится названием файла
	// Объединяется путь из env файла с названием файла
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05.000000")
	logFilePath := filepath.Join(
		config.Folder,
		fmt.Sprintf("%s.log", timestamp),
	)

	/*
		Открытие файла
		os.O_CREATE - Создать файл, если он отсутствует.
		os.O_WRONLY - Открыть файл только для записи.
	*/
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("fail to open log file: %w", err)
	}

	// Настройка внешнего вида логов. Она определяет:
	// как отображать время, как отображать уровень,
	// как называть пол, как выводить сообщения и stack trace.
	zapConfig := zap.NewDevelopmentEncoderConfig()

	// Переопределяется формат времени внутри каждой записи лога.
	zapConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02T15-04-05.000000")

	// JSON is the production default so collectors can parse stable fields.
	// Console remains available for local interactive development.
	var zapEncoder zapcore.Encoder
	if config.Format == "console" {
		zapEncoder = zapcore.NewConsoleEncoder(zapConfig)
	} else {
		zapEncoder = zapcore.NewJSONEncoder(zapConfig)
	}

	/*
		Создание двух направлений записи. NewTee объединяет несколько logger core.
		core в Zap отвечает на три вопроса:
		1. как форматировать запись;
		2. куда её записывать;
		3. какие уровни разрешены.
	*/
	core := zapcore.NewTee(
		zapcore.NewCore(zapEncoder, zapcore.AddSync(os.Stdout), zapLvl),
		zapcore.NewCore(zapEncoder, zapcore.AddSync(logFile), zapLvl),
	)

	/*
		Из настроенного core создаётся готовый *zap.Logger.
		zap.AddCaller() - Из настроенного core создаётся готовый *zap.Logger.
	*/
	zapLogger := zap.New(core, zap.AddCaller())

	return &Logger{
		Logger: zapLogger,
		file:   logFile,
	}, nil

}

// Кладет логгер в контекст
func ToContext(ctx context.Context, log *Logger) context.Context {
	return context.WithValue(
		ctx, // существующий контекст
		key, // ключ для поиска логера
		log, // logger который мы хотим сохранить в контексте
	)
}

// Достает логгер из контекста
func FromContext(ctx context.Context) *Logger {
	// Проверь, является ли полученное значение указателем *Logger.
	// Если да — верни его как *Logger.
	log, ok := ctx.Value(key).(*Logger) // Это type assertion — утверждение типа.
	if !ok {
		panic("no logger in context")
	}

	return log
}

/*
Создаётся производный logger, постоянно содержащий поля
текущего запроса:

При добавлении таких ключ - значений:
request_id = abc-123

При вызове логера - Info("health check started")

Вывод содержит структурированное поле request_id без raw URL, query string,
заголовков авторизации и тела запроса.
*/
func (l *Logger) With(field ...zap.Field) *Logger {
	return &Logger{
		Logger: l.Logger.With(field...),
		file:   l.file,
	}
}

// Закрытие файла для логов
func (l *Logger) Close() {
	if l == nil || l.file == nil {
		return
	}

	if err := l.file.Close(); err != nil {
		fmt.Println("failed to close logger", err)
	}
}
