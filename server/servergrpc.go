package main

// Импортируем необходимые пакеты
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	_ "github.com/mattn/go-sqlite3" // Драйвер для базы данных SQLite
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	pb "YandexGO_Arithmetic_v2/grpc"
)

type agentServer struct {
	pb.UnimplementedAgentServiceServer
	agents map[string]*Agent
	mu     sync.Mutex
}

func (s *agentServer) Connect(ctx context.Context, req *pb.ConnectRequest) (*pb.ConnectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent := &Agent{
		ID:       req.Id,
		Active:   true,
		LastSeen: time.Now(),
	}
	s.agents[req.Id] = agent
	return &pb.ConnectResponse{}, nil
}

func (s *agentServer) GetAgents(req *pb.GetAgentsRequest, stream pb.AgentService_GetAgentsServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, agent := range s.agents {
		agentPB := &pb.Agent{
			Id:       agent.ID,
			Active:   agent.Active,
			LastSeen: timestamppb.New(agent.LastSeen),
		}
		if err := stream.Send(agentPB); err != nil {
			return err
		}
	}
	return nil
}

func (s *agentServer) UpdateAgentStatus(ctx context.Context, req *pb.UpdateAgentStatusRequest) (*pb.UpdateAgentStatusResponse, error) {
	agent, ok := s.agents[req.Id]
	if !ok {
		return nil, errors.New("agent not found")
	}
	agent.Active = req.Active
	return &pb.UpdateAgentStatusResponse{}, nil
}

type Agent struct {
	ID       string
	Active   bool
	LastSeen time.Time
}

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Определяем глобальные переменные
var (
	mu     sync.Mutex
	agents = make(map[string]*Agent)
	db     *sql.DB
	jwtKey = []byte("your_secret_key")
)

// Определяем функцию init, которая инициализирует базу данных при запуске программы
func init() {
	// Открываем файл базы данных SQLite или создаем его, если он не существует
	var err error
	db, err = sql.Open("sqlite3", "./calc.db")
	if err != nil {
		log.Fatal(err)
	}

	// Создаем таблицу users, если она не существует, с полями id, username, password
	sqlStmt := `
create table if not exists users (id integer not null primary key, username text, password text);
`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Fatal(err)
	}
	// Создаем пользователя по умолчанию
	res, err := db.Exec("INSERT OR ROLLBACK INTO users (id, username, password) VALUES (?,?,?)", 1, "admin", "admin")
	// IGNORE: Пропускает ошибку при попытке вставить пользователя, если он уже существует.
	// FAIL: Прерывает текущий SQL запрос при возникновении конфликта, но не отменяет транзакцию.
	// ROLLBACK: Отменяет текущую транзакцию при возникновении конфликта.
	if err != nil {
		// log.Fatal(err) // Вывод прерывает дальнейшее выполнение программы
		log.Printf("Пользователь admin уже существует\n")
	}

	// Создаем таблицу calc, если она не существует, с полями id, calc, stat и res, agent, t1, t2, id_user
	sqlStmt = `
create table if not exists calc (id integer not null primary key, calc text, stat text, res text, agent text, t1 text, t2 text,
id_user integer, foreign key(id_user) references users(id));
`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Fatal(err)
	}

	res, err = db.Exec("UPDATE calc SET stat = 'ожидание', agent = 'нет' WHERE stat = 'в работе'")
	if err != nil {
		log.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Изменено %d строк\n", n)

	// Создаем таблицу oper, если она не существует, с полями id, oper, symbol и time, id_user
	sqlStmt = `
create table if not exists oper (id integer not null primary key, oper text, symbol text, time integer);
`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Fatal("Error ", err)
	}

	// Подготавливаем запрос для вставки данных в таблицу oper
	stmt, err := db.Prepare("insert into oper(id, oper, symbol, time) values(?, ?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	// Закрываем запрос по завершении функции
	defer stmt.Close()
	// Создаем срез с первоначальными данными для вставки
	data := [][]interface{}{
		{1, "plus", "+", 10},
		{2, "minus", "-", 10},
		{3, "multiply", "*", 10},
		{4, "divide", "/", 10},
	}
	// Подготавливаем запрос для проверки, пуста ли таблица oper
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM oper").Scan(&count)
	if err != nil {
		log.Fatal(err)
	}

	// Проверяем, равно ли количество нулю
	if count == 0 {
		// Если да, значит таблица пуста, и мы можем добавить данные
		// В цикле выполняем запрос с данными из среза
		for _, row := range data {
			_, err = stmt.Exec(row...)
			if err != nil {
				log.Fatal(err)
			}
		}
		// Выводим сообщение об успешном заполнении таблицы
		log.Println("Таблица oper заполнена первоначальными данными")
	} else {
		// Если нет, значит таблица не пуста, и мы пропускаем добавление данных
		log.Println("Таблица oper не пуста, добавление данных пропущено")
	}

}

// Определяем функцию calcHandler, которая обрабатывает запросы по адресу /calc?=
func calcHandler(w http.ResponseWriter, r *http.Request) {
	// Извлекаем данные пользователя из контекста который был передан из Middleware
	user_id := r.Context().Value("user_id")
	// username := r.Context().Value("username")

	// Получаем арифметическое выражение из параметра запроса
	expr := r.URL.Query().Get("calc")
	// Проверяем, не пусто ли выражение
	if expr == "" {
		// Если да, выводим сообщение об ошибке
		fmt.Fprintln(w, "Нет выражения для вычисления")
		return
	}

	// Устанавливаем статусы
	stat := "ожидание"
	res := "нет"
	agent := "нет"
	// Время создание задачи
	// Получаем текущую дату и время
	now := time.Now()
	dat := now.Format("02.01.2006") // формат даты - день.месяц.год
	tme := now.Format("15:04:05")   // формат времени - часы:минуты:секунды
	t1 := dat + " " + tme
	t2 := "-----"

	// Подготавливаем запрос для вставки данных в таблицу calc
	stmt, err := db.Prepare("insert into calc(calc, stat, res, agent, t1, t2, id_user) values(?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	// Закрываем запрос по завершении функции
	defer stmt.Close()
	// Выполняем запрос с переданными данными и получаем результат
	result, err := stmt.Exec(expr, stat, res, agent, t1, t2, user_id)
	if err != nil {
		log.Fatal(err)
	}
	// Получаем id добавленной строки
	id, err := result.LastInsertId()
	if err != nil {
		log.Fatal(err)
	}
	// Выводим сообщение об успешном сохранении выражения и его id
	fmt.Fprintf(w, "Выражение %s успешно сохранено в базе данных.\nID выражения: %d\n", expr, id)
}

// Определяем функцию listHandler, которая обрабатывает запросы по адресу /list
func listHandler(w http.ResponseWriter, r *http.Request) {
	// Извлекаем данные пользователя из контекста который был передан из Middleware
	user_id := r.Context().Value("user_id")
	username := r.Context().Value("username")

	// Подготавливаем запрос для выборки всех данных из таблицы calc
	rows, err := db.Query("select id, calc, stat, res, agent, t1, t2 from calc where id_user = ?", user_id)
	if err != nil {
		log.Fatal(err)
	}
	// Закрываем запрос по завершении функции
	defer rows.Close()
	fmt.Fprintf(w, "ID user: %v \nUsername: %s \n", user_id, username)
	// Выводим заголовок таблицы
	fmt.Fprintln(w, "| id | calc | stat | result | agent | t1 | t2 |")
	fmt.Fprintln(w, "|----|------|------|--------|-------|----|----|")
	// В цикле читаем данные из запроса
	for rows.Next() {
		// Объявляем переменные для хранения данных
		var id int
		var calc, stat, res, agent, t1, t2 string
		// Сканируем данные в переменные
		err = rows.Scan(&id, &calc, &stat, &res, &agent, &t1, &t2)
		if err != nil {
			log.Fatal(err)
		}
		// Выводим данные в виде строки таблицы
		fmt.Fprintf(w, "| %d | %s | %s | %s | %s | %s | %s |\n", id, calc, stat, res, agent, t1, t2)
	}
	// Проверяем, не произошла ли ошибка при чтении данных
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}
}

// Определяем функцию idHandler, которая обрабатывает запросы по адресу /id=
func idHandler(w http.ResponseWriter, r *http.Request) {
	// Извлекаем данные пользователя из контекста который был передан из Middleware
	user_id := r.Context().Value("user_id")
	// username := r.Context().Value("username")

	// Получаем идентификатор выражения из параметра запроса
	id := r.URL.Query().Get("num")
	// Проверяем, не пусто ли идентификатор
	if id == "" {
		// Если да, выводим сообщение об ошибке
		fmt.Fprintln(w, "Нет идентификатора для поиска")
		return
	}
	// Подготавливаем запрос для выборки данных из таблицы calc по заданному идентификатору
	stmt, err := db.Prepare("select id, calc, stat, res, agent, t1, t2 from calc where id = ? and id_user = ?")
	if err != nil {
		log.Fatal(err)
	}
	// Закрываем запрос по завершении функции
	defer stmt.Close()
	// Выполняем запрос с переданным идентификатором
	row := stmt.QueryRow(id, user_id)
	// Объявляем переменные для хранения данных
	var idNum int
	var calc, stat, res, agent, t1, t2 string
	// Сканируем данные в переменные
	err = row.Scan(&idNum, &calc, &stat, &res, &agent, &t1, &t2)
	// Проверяем, не произошла ли ошибка при сканировании данных
	if err != nil {
		// Если да, проверяем, не было ли это из-за отсутствия данных
		if err == sql.ErrNoRows {
			// Если да, выводим сообщение, что данные не найдены
			fmt.Fprintln(w, "Данные не найдены. \nВведен не верный id. \nСписок ваших задач с id можно увидеть выполнив команду /list")
		} else {
			// Если нет, выводим сообщение об ошибке
			log.Fatal(err)
		}
		// Возвращаемся из функции
		return
	}
	// Выводим данные в виде строки
	fmt.Fprintf(w, "id: %d\ncalc: %s\nstat: %s\nres: %s\nagent: %s\nt1: %s\nt2: %s\n", idNum, calc, stat, res, agent, t1, t2)
}

// Определяем функцию operHandler, которая обрабатывает запросы по адресу /oper
func operHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем значение параметров из запроса
	plus := r.URL.Query().Get("plus")
	minus := r.URL.Query().Get("minus")
	multiply := r.URL.Query().Get("multiply")
	divide := r.URL.Query().Get("divide")
	// Проверяем, не пусто ли значение
	if plus != "" {
		// Если нет, значит запрос с параметром, и мы обновляем данные в таблице oper
		// Преобразуем значение в целое число
		time, err := strconv.Atoi(plus)
		if err != nil {
			// Если произошла ошибка, выводим сообщение об ошибке
			fmt.Fprintln(w, "Неверное значение для обновления")
			return
		}
		// Подготавливаем запрос для обновления данных в таблице oper
		stmt, err := db.Prepare("update oper set time = ? where oper = ?")
		if err != nil {
			log.Fatal(err)
		}
		// Закрываем запрос по завершении функции
		defer stmt.Close()
		// Выполняем запрос с переданными значениями
		_, err = stmt.Exec(time, "plus")
		if err != nil {
			log.Fatal(err)
		}
		// Выводим сообщение об успешном обновлении данных
		fmt.Fprintf(w, "Данные в таблице oper обновлены.\n")
	} else if minus != "" {
		// Если нет, значит запрос с параметром, и мы обновляем данные в таблице oper
		// Преобразуем значение в целое число
		time, err := strconv.Atoi(minus)
		if err != nil {
			// Если произошла ошибка, выводим сообщение об ошибке
			fmt.Fprintln(w, "Неверное значение для обновления")
			return
		}
		// Подготавливаем запрос для обновления данных в таблице oper
		stmt, err := db.Prepare("update oper set time = ? where oper = ?")
		if err != nil {
			log.Fatal(err)
		}
		// Закрываем запрос по завершении функции
		defer stmt.Close()
		// Выполняем запрос с переданными значениями
		_, err = stmt.Exec(time, "minus")
		if err != nil {
			log.Fatal(err)
		}
		// Выводим сообщение об успешном обновлении данных
		fmt.Fprintf(w, "Данные в таблице oper обновлены.\n")
	} else if multiply != "" {
		// Если нет, значит запрос с параметром, и мы обновляем данные в таблице oper
		// Преобразуем значение в целое число
		time, err := strconv.Atoi(multiply)
		if err != nil {
			// Если произошла ошибка, выводим сообщение об ошибке
			fmt.Fprintln(w, "Неверное значение для обновления")
			return
		}
		// Подготавливаем запрос для обновления данных в таблице oper
		stmt, err := db.Prepare("update oper set time = ? where oper = ?")
		if err != nil {
			log.Fatal(err)
		}
		// Закрываем запрос по завершении функции
		defer stmt.Close()
		// Выполняем запрос с переданными значениями
		_, err = stmt.Exec(time, "multiply")
		if err != nil {
			log.Fatal(err)
		}
		// Выводим сообщение об успешном обновлении данных
		fmt.Fprintf(w, "Данные в таблице oper обновлены.\n")
	} else if divide != "" {
		// Если нет, значит запрос с параметром, и мы обновляем данные в таблице oper
		// Преобразуем значение в целое число
		time, err := strconv.Atoi(divide)
		if err != nil {
			// Если произошла ошибка, выводим сообщение об ошибке
			fmt.Fprintln(w, "Неверное значение для обновления")
			return
		}
		// Подготавливаем запрос для обновления данных в таблице oper
		stmt, err := db.Prepare("update oper set time = ? where oper = ?")
		if err != nil {
			log.Fatal(err)
		}
		// Закрываем запрос по завершении функции
		defer stmt.Close()
		// Выполняем запрос с переданными значениями
		_, err = stmt.Exec(time, "divide")
		if err != nil {
			log.Fatal(err)
		}
		// Выводим сообщение об успешном обновлении данных
		fmt.Fprintf(w, "Данные в таблице oper обновлены.\n")
	} else {
		// Если да, значит запрос без параметра, и мы выводим таблицу oper
		// Подготавливаем запрос для выборки всех данных из таблицы oper
		rows, err := db.Query("select id, oper, symbol, time from oper")
		if err != nil {
			log.Fatal(err)
		}
		// Закрываем запрос по завершении функции
		defer rows.Close()

		// Выводим заголовок таблицы
		fmt.Fprintln(w, "| id | oper | symbol | time |")
		fmt.Fprintln(w, "|----|------|--------|------|")
		// В цикле читаем данные из запроса
		for rows.Next() {
			// Объявляем переменные для хранения данных
			var id, time int
			var oper, symbol string
			// Сканируем данные в переменные
			err = rows.Scan(&id, &oper, &symbol, &time)
			if err != nil {
				log.Fatal(err)
			}
			// Выводим данные в виде строки таблицы
			fmt.Fprintf(w, "| %d | %s | %s | %d |\n", id, oper, symbol, time)
		}
		// Проверяем, не произошла ли ошибка при чтении данных
		err = rows.Err()
		if err != nil {
			log.Fatal(err)
		}
	}
}

// Определяем функцию main, которая запускает веб-сервер
func main() {
	go func() {
		// Регистрируем функцию calcHandler для обработки запросов по адресу /?calc=
		http.Handle("/", MiddlewareJWT(http.HandlerFunc(calcHandler)))
		// Регистрируем функцию listHandler для обработки запросов по адресу /list
		http.Handle("/list", MiddlewareJWT(http.HandlerFunc(listHandler)))
		// Регистрируем функцию idHandler для обработки запросов по адресу /id?num=
		http.Handle("/id", MiddlewareJWT(http.HandlerFunc(idHandler)))
		// Регистрируем функцию operHandler для обработки запросов по адресу /oper
		http.Handle("/oper", MiddlewareJWT(http.HandlerFunc(operHandler)))

		http.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
			id := r.URL.Query().Get("id")
			mu.Lock()
			agents[id] = &Agent{ID: id, Active: true, LastSeen: time.Now()}
			mu.Unlock()
		})
		http.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
			// Для старых агентов
			/* mu.Lock()
			for _, agent := range agents {
				fmt.Fprintf(w, "ID: %s, Active: %v, LastSeen: %s\n", agent.ID, agent.Active, agent.LastSeen)
			}
			mu.Unlock() */

			// Для агентов gRPC
			// Создаем gRPC клиент
			conn, err := grpc.Dial("localhost:8081", grpc.WithInsecure())
			if err != nil {
				http.Error(w, "Could not connect to gRPC server", http.StatusInternalServerError)
				return
			}
			defer conn.Close()

			// Инициализируем gRPC клиент
			client := pb.NewAgentServiceClient(conn)

			// Вызываем метод GetAgents
			resp, err := client.GetAgents(context.Background(), &pb.GetAgentsRequest{})
			if err != nil {
				http.Error(w, "Failed to get agents from gRPC server", http.StatusInternalServerError)
				return
			}

			// Пишем данные агентов в ответ HTTP
			for {
				agent, err := resp.Recv()
				if err != nil {
					break
				}
				fmt.Fprintf(w, "ID: %s, Active: %v, LastSeen: %s\n", agent.Id, agent.Active, agent.LastSeen)
			}
		})

		// Регистрация, авторизация

		http.HandleFunc("/reg", registerHandler)
		http.HandleFunc("/auth", authHandler)

		// Запускаем веб-сервер на порту 8080
		log.Println("Сервер запущен на порту 8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	go checkAgents()
	go checkAgentsgRPC()

	// Создаем прослушиватель для gRPC сервера
	grpcListener, err := net.Listen("tcp", ":8081")
	if err != nil {
		log.Fatalf("Failed to create gRPC listener: %v", err)
	}

	// Запускаем gRPC сервер
	grpcServer := grpc.NewServer()
	pb.RegisterAgentServiceServer(grpcServer, &agentServer{
		agents: make(map[string]*Agent),
	})
	log.Println("gRPC Сервер запущен на порту 8081")
	log.Fatal(grpcServer.Serve(grpcListener))

}

// Чек для старых агентов
func checkAgents() {
	ticker := time.NewTicker(5 * time.Second)
	for range ticker.C {
		mu.Lock()
		for id, agent := range agents {
			if time.Since(agent.LastSeen) > 10*time.Second {
				agent.Active = false
				_, err := db.Exec("UPDATE calc SET stat = 'ожидание', agent = 'нет' WHERE res = 'нет' AND agent = ?", id)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
		mu.Unlock()
	}
}

// Чек агентов gRPC
func checkAgentsgRPC() {
	ticker := time.NewTicker(5 * time.Second)
	for range ticker.C {
		// Создаем контекст с таймаутом
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Создаем gRPC клиент с контекстом
		conn, err := grpc.DialContext(ctx, "localhost:8081", grpc.WithInsecure())
		if err != nil {
			log.Printf("Could not connect to gRPC server: %v", err)
			continue
		}
		defer conn.Close()

		// Инициализируем gRPC клиент
		client := pb.NewAgentServiceClient(conn)

		// Вызываем метод GetAgents
		resp, err := client.GetAgents(context.Background(), &pb.GetAgentsRequest{})
		if err != nil {
			log.Fatalf("Failed to get agents from gRPC server: %v", err)
		}

		// Обрабатываем полученных агентов
		for {
			agent, err := resp.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				log.Fatalf("Error receiving agent: %v", err)
			}

			// Проверяем активность агента
			if time.Since(agent.LastSeen.AsTime()) > 10*time.Second {
				// Агент неактивен, изменяем его статус на сервере
				_, err = db.Exec("UPDATE calc SET stat = 'ожидание', agent = 'нет' WHERE res = 'нет' AND agent = ?", agent.Id)
				if err != nil {
					log.Fatal(err)
				}
				_, err := client.UpdateAgentStatus(context.Background(), &pb.UpdateAgentStatusRequest{
					Id:     agent.Id,
					Active: false,
				})
				if err != nil {
					log.Fatalf("Failed to update agent status on gRPC server: %v", err)
				}
			}
		}

		// Теперь можно сделать что-то еще с агентами, если нужно
	}
}

// Регистрация, авторизация

func registerUser(db *sql.DB, username, password string) (int64, error) {
	// Регистрируем пользователя
	statement, err := db.Prepare("INSERT INTO users(username, password) VALUES(?, ?)")
	if err != nil {
		return 0, err
	}
	result, err := statement.Exec(username, password)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()

	/* зря сделал
	// Создаем первоначальные таймауты на выполнение арифметических действий в таблице oper для нового пользователя
	// Подготавливаем запрос для вставки данных в таблицу oper
	stmt, err := db.Prepare("insert into oper(oper, symbol, time, id_user) values(?, ?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	// Закрываем запрос по завершении функции
	defer stmt.Close()
	// Создаем срез с первоначальными данными для вставки
	data := [][]interface{}{
		{"plus", "+", 10, id},
		{"minus", "-", 10, id},
		{"multiply", "*", 10, id},
		{"divide", "/", 10, id},
	}

	// В цикле выполняем запрос с данными из среза
	for _, row := range data {
		_, err = stmt.Exec(row...)
		if err != nil {
			log.Fatal(err)
		}
	}
	// Выводим сообщение об успешном заполнении таблицы
	log.Println("Таблица oper заполнена первоначальными данными")
	*/
	return id, err
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("login")
	password := r.URL.Query().Get("password")

	id_user, err := registerUser(db, username, password)
	if err != nil {
		http.Error(w, "Error registering user", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "ID: %d. Username: %s - registered successfully.\n", id_user, username)
	// w.Write([]byte("User registered successfully"))
}

func authHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("login")
	password := r.URL.Query().Get("password")

	// Проверяем логин и пароль в базе данных
	var user User
	err := db.QueryRow("SELECT id, username, password FROM users WHERE username = ? AND password = ?", username, password).Scan(&user.ID, &user.Username, &user.Password)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Invalid login or password", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Генерируем JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Username,
		"exp":      time.Now().Add(time.Minute * 2).Unix(),
	})

	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:    "token",
		Value:   tokenString,
		Expires: time.Now().Add(time.Minute * 2),
	})

	fmt.Fprintln(w, "Authenticated")
	fmt.Fprintf(w, "ID user: %v \nUsername: %s \n", user.ID, user.Username)
	// json.NewEncoder(w).Encode(map[string]string{"message": "Authenticated"})
}

// MiddlewareJWT проверяет JWT в cookies запроса.
func MiddlewareJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("token")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		tokenString := cookie.Value // Получаем значение cookie в виде строки

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtKey, nil
		})

		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			// Создаем новый контекст с данными пользователя
			ctx := context.WithValue(r.Context(), "user_id", claims["user_id"])
			ctx = context.WithValue(ctx, "username", claims["username"])

			// Создаем новый запрос с обновленным контекстом
			r = r.WithContext(ctx)
			// JWT валиден, передаем запрос дальше
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
	})
}
