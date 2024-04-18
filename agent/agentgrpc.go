package main

import (
	pb "YandexGO_Arithmetic_v2/grpc"
	"context"
	"database/sql"
	"fmt"
	"github.com/Knetic/govaluate"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"log"
	// "net/http"
	"sync/atomic"
	"time"
)

func evaluateWithDelays(expression *govaluate.EvaluableExpression) (interface{}, error) {
	// Создаем мапу operators для хранения символов операций и их задержек
	operators := make(map[string]time.Duration)

	db, err := sql.Open("sqlite3", "./calc.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Выполняем запрос к базе данных, выбирая все символы операций и их задержки из таблицы oper
	rows, err := db.Query("select symbol, time from oper")
	if err != nil {
		fmt.Println(err)
	}
	defer rows.Close()

	// Перебираем все строки результата
	for rows.Next() {
		// Считываем значения из каждой строки
		var symbol string
		var delay int
		err := rows.Scan(&symbol, &delay)
		if err != nil {
			fmt.Println(err)
			continue
		}

		// Добавляем значения в мапу operators, используя ключом символ операции, а значением задержку, умноженную на time.Second
		operators[symbol] = time.Duration(delay) * time.Second
	}

	tokens := expression.Tokens()
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if operator, ok := token.Value.(string); ok {
			if delay, ok := operators[operator]; ok {
				time.Sleep(delay)
			}
		}
	}

	result, err := expression.Evaluate(nil)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func main() {
	db, err := sql.Open("sqlite3", "./calc.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ticker := time.NewTicker(1 * time.Second)
	sem := make(chan struct{}, 2) // Семафор для ограничения количества горутин
	var usedSlots int32           // Переменная для отслеживания количества используемых слотов
	for range ticker.C {
		var id, calc string
		fmt.Println(usedSlots)
		if usedSlots < 2 {
			err := db.QueryRow("SELECT id, calc FROM calc WHERE stat = 'ожидание' LIMIT 1").Scan(&id, &calc)
			if err != nil {
				if err == sql.ErrNoRows {
					//----//

					conn, err := grpc.Dial("localhost:8081", grpc.WithInsecure()) // Подключение к gRPC серверу
					if err != nil {
						log.Fatalf("could not connect: %v", err)
					}
					defer conn.Close()
					client := pb.NewAgentServiceClient(conn)

					// Вызов метода Connect
					connectResponse, err := client.Connect(context.Background(), &pb.ConnectRequest{Id: "agentgrpc"})
					if err != nil {
						log.Fatalf("Connect failed: %v", err)
					}
					log.Printf("Connect response: %v", connectResponse)

					/*resp, err := http.Get("http://localhost:8080/connect?id=agent1")
					if err != nil {
						fmt.Println("Ошибка:", err)
						continue
					}
					resp.Body.Close()*/
					fmt.Println("Нет задач в ожидании. Статус отправлен")
					continue
				} else {
					log.Fatal(err)
				}
			}
		} else {
			conn, err := grpc.Dial("localhost:8081", grpc.WithInsecure()) // Подключение к gRPC серверу
			if err != nil {
				log.Fatalf("could not connect: %v", err)
			}
			defer conn.Close()
			client := pb.NewAgentServiceClient(conn)

			// Вызов метода Connect
			connectResponse, err := client.Connect(context.Background(), &pb.ConnectRequest{Id: "agentgrpc"})
			if err != nil {
				log.Fatalf("Connect failed: %v", err)
			}
			log.Printf("Connect response: %v", connectResponse)
			/* resp, err := http.Get("http://localhost:8080/connect?id=agent1")
			if err != nil {
				fmt.Println("Ошибка:", err)
				continue
			}
			resp.Body.Close() */
			fmt.Println("Статус отправлен.")
			continue
		}

		_, err = db.Exec("UPDATE calc SET stat = 'в работе', agent = 'agentgrpc' WHERE id = ?", id)
		if err != nil {
			log.Fatal(err)
		}

		// Вычисляем выражение в отдельной горутине
		go func(id, calc string) {
			sem <- struct{}{}              // Захватываем слот в семафоре
			atomic.AddInt32(&usedSlots, 1) // Увеличиваем счетчик используемых слотов
			defer func() {
				<-sem
				atomic.AddInt32(&usedSlots, -1) // Уменьшаем счетчик используемых слотов
			}() // Освобождаем слот при завершении горутины

			// Вычисляем выражение
			expression, err := govaluate.NewEvaluableExpression(calc)
			if err != nil {
				fmt.Println("Ошибка при создании выражения:", err)
				// Записываем ошибку в базу данных
				_, err = db.Exec("UPDATE calc SET res = ?, stat = 'ошибка', t2 = '------' WHERE id = ?", "нет", id)
				if err != nil {
					log.Fatal(err)
				}
				return
			}

			result, err := evaluateWithDelays(expression)
			if err != nil {
				fmt.Println("Ошибка при вычислении выражения:", err)
				// Записываем ошибку в базу данных
				_, err = db.Exec("UPDATE calc SET res = ?, stat = 'ошибка', t2 = '-----' WHERE id = ?", "нет", id)
				if err != nil {
					log.Fatal(err)
				}
				return
			}

			// Записываем результат в базу данных
			// Получаем текущую дату и время
			now := time.Now()
			dat := now.Format("02.01.2006") // формат даты - день.месяц.год
			tme := now.Format("15:04:05")   // формат времени - часы:минуты:секунды
			t2 := dat + " " + tme
			_, err = db.Exec("UPDATE calc SET res = ?, stat = 'завершено', t2 = ? WHERE id = ?", result, t2, id)
			if err != nil {
				log.Fatal(err)
			}
		}(id, calc)

		conn, err := grpc.Dial("localhost:8081", grpc.WithInsecure()) // Подключение к gRPC серверу
		if err != nil {
			log.Fatalf("could not connect: %v", err)
		}
		defer conn.Close()
		client := pb.NewAgentServiceClient(conn)

		// Вызов метода Connect
		connectResponse, err := client.Connect(context.Background(), &pb.ConnectRequest{Id: "agentgrpc"})
		if err != nil {
			log.Fatalf("Connect failed: %v", err)
		}
		log.Printf("Connect response: %v", connectResponse)
		/* resp, err := http.Get("http://localhost:8080/connect?id=agent1")
		if err != nil {
			fmt.Println("Ошибка:", err)
			continue
		}
		resp.Body.Close()*/
		fmt.Println("Статус отправлен, получена задача:", calc)
	}
}
