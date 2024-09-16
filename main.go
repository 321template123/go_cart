package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
)

// Карта страниц. Хранит в себе по ключам(Страница пагинации) - дату последнего запроса на эту страницу
var pages map[string]time.Time = make(map[string]time.Time)
var PAGES_TIME_TO_SAVE int = 24
var PAGES_TIME_TO_SAVE_FACTOR float64 = time.Hour.Minutes()
var PRODUCT_PAGE_PREFIX string = "product-page-"

// Карта клиентов. Хранит в себе по ключам(Страница пагинации) - дату последнего запроса данных
var clients map[string]time.Time = make(map[string]time.Time)
var CLIENTS_TIME_TO_SAVE int = 24
var CLIENTS_TIME_TO_SAVE_FACTOR float64 = time.Hour.Minutes()

// Контекст подключения, так надо говорят
var redisCtx = context.Background()

// Указатель на экземпляр подключения
var redisConnect *redis.Client

func main() {
	// Подключаемся. Пытаемся хотя-бы
	redisConnect = redis.NewClient(&redis.Options{
		Addr:     "192.168.0.81:6379", // Адрес сервера Redis
		Password: "",                  // Пароль, если есть
		DB:       0,                   // Номер базы данных
	})

	// Тут просто отчищаем ключи
	// keys, err := redisConnect.Keys(redisCtx, "*").Result()
	// if err != nil {
	// 	log.Fatalf("Ошибка получения ключей: %v", err)
	// }
	// redisConnect.Del(redisCtx, keys...).Result()
	// fmt.Println("Список всех ключей:")
	// for _, key := range keys {
	// 	fmt.Println(key)
	// }

	// Сервер запускаем в Горутине
	go startServer()

	// Бесконечный цикл для кручения
	for {
		time.Sleep(10000 * time.Second)
	}
}

func startServer() {
	http.HandleFunc("/api/product", product)
	err := http.ListenAndServe(":8090", nil)
	if err != nil {
		fmt.Println("Ошибка! Сервер не удалось запустить")
	}
}

// Функция обрабатывает запрос продуктов
func product(w http.ResponseWriter, r *http.Request) {
	// Получаем номер запрашиваемой страницы (Для пагинации)
	query_page := r.URL.Query().Get("page")
	// Если страница не пришла, значит ставим базовое значение 0
	if len(query_page) == 0 {
		query_page = "0"
	}
	// Из карты страниц запрашиваем соответсвующую
	update, state := pages[query_page]
	// Если такой не существует, значит нужно создать
	if !state {
		// Создаём запись с текущим временем
		pages[query_page] = time.Now()
		// Запрашиваем данные и тут же их возвращаем в виде ответа
		requestJson(w, getProduct(query_page))
		return
	}

	// Если запись существовала
	currentTime := time.Now()
	// Определяем как давно она существует
	sub := currentTime.Sub(update).Seconds()
	// Тут же обновляем, текущее обращение
	pages[query_page] = currentTime

	// Если запись хранилась дольше n-секунд, то обновляем, перезаписываем cache
	if sub >= 60 {
		fmt.Println("From BD")
		requestJson(w, getProduct(query_page))
	} else {
		// Если же меньше, значит нужно найти такую запись в Redis
		fmt.Println("From CACHE")
		requestJson(w, getProductFromRedis(query_page))
	}
}

// Функция возвращает ответ в виде JSON
func requestJson(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(data))
}

func getProduct(query_page string) string {
	// URL для GET-запроса
	url := "https://fakerapi.it/api/v2/books?_quantity=10"

	// Отправляем GET-запрос
	response, err := http.Get(url)
	if err != nil {
		log.Fatalf("Ошибка при отправке GET-запроса: %v", err)
	}
	defer response.Body.Close()

	// Читаем тело ответа
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatalf("Ошибка при чтении ответа: %v", err)
	}

	_, err = redisConnect.Ping(redisCtx).Result()
	if err == nil {
		_, err = redisConnect.Set(redisCtx, PRODUCT_PAGE_PREFIX+query_page, string(body), 0).Result()
		fmt.Println("Записываем в " + PRODUCT_PAGE_PREFIX + query_page + "полученные данные")
	}
	// Выводим ответ на экран
	return string(body)
}

func getProductFromRedis(query_page string) string {
	_, err := redisConnect.Ping(redisCtx).Result()
	if err != nil {
		return getProduct(query_page)
	}
	val, err := redisConnect.Get(redisCtx, fmt.Sprintf(PRODUCT_PAGE_PREFIX+query_page)).Result()
	if err != nil {
		return getProduct(query_page)
	}
	return val
}
