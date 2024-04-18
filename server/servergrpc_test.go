package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCalcHandler(t *testing.T) {

	// добавление данных в базу данных
	t.Run("valid expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/?calc=1*2", nil)

		// вызов тестируемой функции
		calcHandler(w, r)

		// проверка результата
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "Выражение 1*2 успешно сохранено в базе данных.\nID выражения: 1\n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

	// проверка на пустое выражение
	t.Run("empty expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/?calc=", nil)

		// вызов тестируемой функции
		calcHandler(w, r)

		// проверка результата
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "Нет выражения для вычисления\n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

}

func TestIdHandler(t *testing.T) {

	// тест получения несуществующей задачи по ее ID
	t.Run("empty expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/id?num=", nil)

		// вызов тестируемой функции
		idHandler(w, r)

		// проверка результата
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "Нет идентификатора для поиска\n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

}

func TestAuthHandler(t *testing.T) {

	// тест успешной авторизации
	t.Run("true expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/auth?login=admin&password=admin", nil)

		// вызов тестируемой функции
		authHandler(w, r)

		// проверка результата
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "Authenticated\nID user: 1 \nUsername: admin \n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

	// тест не успешной авторизации
	t.Run("false expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/auth?login=admin&password=admin1", nil)

		// вызов тестируемой функции
		authHandler(w, r)

		// проверка результата
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "Invalid login or password\n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

}

func TestRegisterHandler(t *testing.T) {

	// тест успешной регистрации
	t.Run("true expression", func(t *testing.T) {
		// создание тестовых объектов
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/reg?login=test&password=test", nil)

		// вызов тестируемой функции
		registerHandler(w, r)

		// проверка результата
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}
		expected := "ID: 2. Username: test - registered successfully.\n"
		if w.Body.String() != expected {
			t.Errorf("Expected '%s', got '%s'", expected, w.Body.String())
		}
	})

}
