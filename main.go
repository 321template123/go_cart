package main

// Работать будет и без Redis, однако будет долго грузить данные и без кэша соответсвенно
import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

// Для nginx
var VERSION_API string = "v1"

var pages map[string]time.Time = make(map[string]time.Time) // Карта страниц. Хранит в себе по ключам(Страница пагинации) - дату последнего запроса на эту страницу
var PAGES_TIME_TO_SAVE float64 = 24                         // Сколько единиц времени будет хранится cache страницы
var PAGES_TIME_TO_SAVE_FACTOR float64 = time.Hour.Minutes() // Модификатор времени
var PRODUCT_PAGE_PREFIX string = "product-page-"            // Префикс для ключей в БД
var pageMutex sync.RWMutex                                  // Хочу спросить/узнать как с этим говном работать куча сомнений

var clients map[string]time.Time = make(map[string]time.Time) // Карта корзин клиентов. Хранит в себе по ключам(sessionId) - дату последнего запроса обновлений или запроса
var CLIENTS_TIME_TO_SAVE float64 = 24                         // Сколько единиц времени будет хранится cache клиента
var CLIENTS_TIME_TO_SAVE_FACTOR float64 = time.Hour.Minutes() // Модификатор времени
var CLIENTS_CART_PREFIX string = "client-cart-"               // Префикс для ключей в БД
var clientMutex sync.RWMutex                                  // Хочу спросить/узнать как с этим говном работать куча сомнений

var redisCtx = context.Background() // Контекст подключения, так надо говорят
var redisConnect *redis.Client      // Указатель на экземпляр подключения

var ADDRESS string = "192.168.0.81:6379" // Адрес моего Redis
var PASSWORD string = ""                 // Базовый пароль
var DATABASE int = 0                     // База данных

func main() {
	// Подключаемся. Ошибку решил не обрабатывать. Так как при отсутсвии Redis всё так-же работает, но дольше.
	redisConnect = redis.NewClient(&redis.Options{
		Addr:     ADDRESS,
		Password: PASSWORD,
		DB:       DATABASE,
	})

	// Здесь мы сканируем наши ключи из Redis и пересоздаём карты.
	// В лучем случае такие карты нужно тоже хранить в БД, а так я просто говорю, что полсе перезапуска все запросы старее на пол часа
	var page_cursor uint64
	for {
		var keys []string
		keys, page_cursor, _ = redisConnect.Scan(redisCtx, page_cursor, PRODUCT_PAGE_PREFIX+"*", 0).Result()
		for _, key := range keys {
			pageMutex.Lock() // Он блокируется
			pages[key] = time.Now().Add(-30 * time.Minute)
			pageMutex.Unlock() // Он деблокируется сразу
		}
		if page_cursor == 0 { // no more keys
			break
		}
	}
	var client_cursor uint64
	for {
		var keys []string
		keys, client_cursor, _ = redisConnect.Scan(redisCtx, client_cursor, CLIENTS_CART_PREFIX+"*", 0).Result()
		for _, key := range keys {
			clientMutex.Lock() // Он блокируется
			clients[key] = time.Now().Add(-30 * time.Minute)
			clientMutex.Unlock() // Он деблокируется сразу
		}
		if client_cursor == 0 { // no more keys
			break
		}
	}
	// redisConnect.Del(redisCtx, keys...).Result()

	// Сервер запускаем в Горутине
	go startServer()
	go garbageCollector()

	// Бесконечный цикл для кручения
	for {
		time.Sleep(10000 * time.Second)
	}
}

// Так как у нас связка будет REACT - NGINX - GO
func startServer() {
	// Обрабатывать будем только несколько состояний
	// Каждый пользователь может запросить список продуктов
	// Каждый пользователь может получить свою корзину
	// Каждый пользователь может обновить свою корзину
	http.HandleFunc(fmt.Sprintf("/%s/api/product", VERSION_API), product)
	http.HandleFunc(fmt.Sprintf("/%s/api/getclientcart", VERSION_API), getClientCart)
	http.HandleFunc(fmt.Sprintf("/%s/api/updateclientcart", VERSION_API), updateClientCart)

	err := http.ListenAndServe(":8090", nil)
	if err != nil {
		fmt.Println("Ошибка! Сервер не удалось запустить")
	}
}

// Получить корзину клиента
func getClientCart(w http.ResponseWriter, r *http.Request) {
	// Получаем его сессию
	session_id := getSession(w, r)
	// Обновляем время доступа к ней
	clientMutex.Lock() // Он блокируется
	clients[CLIENTS_CART_PREFIX+session_id] = time.Now()
	clientMutex.Unlock() // Он деблокируется сразу

	val, _ := redisConnect.Get(redisCtx, fmt.Sprintf(CLIENTS_CART_PREFIX+session_id)).Result()
	requestJson(w, val)
}

// Обновляем состояние сессии
func updateClientCart(w http.ResponseWriter, r *http.Request) {
	session_id := getSession(w, r)
	clientMutex.Lock() // Он блокируется
	clients[CLIENTS_CART_PREFIX+session_id] = time.Now()
	clientMutex.Unlock() // Он деблокируется сразу

	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusBadRequest)
		return
	}

	var postData map[string]interface{} = make(map[string]interface{})
	err := json.NewDecoder(r.Body).Decode(&postData)
	if err != nil {
		http.Error(w, "Invalid request data", http.StatusBadRequest)
		return
	}
	bytes, _ := json.Marshal(postData)
	redisConnect.Set(redisCtx, CLIENTS_CART_PREFIX+session_id, string(bytes), 0)
}

// Получить сессию
func getSession(w http.ResponseWriter, r *http.Request) string {
	// Получить cookie
	myCookie, err := r.Cookie("session_id")
	// Если cookie нет, то устанавливаем новые. Старые куки потом удалятся.
	if err != nil {
		fmt.Println("Куки не найдены")
		return createSession(w).Value
	}
	return myCookie.Value
}

// Создать сессию
func createSession(w http.ResponseWriter) *http.Cookie {
	id := uuid.NewString()

	var cookie http.Cookie = http.Cookie{
		Name:     "session_id",
		Value:    id,
		Expires:  time.Now().AddDate(0, 0, 1),
		Secure:   true,
		HttpOnly: true,
	}

	http.SetCookie(w, &cookie)
	return &cookie
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
	pageMutex.Lock() // Он блокируется
	pages[PRODUCT_PAGE_PREFIX+query_page] = time.Now()
	pageMutex.Unlock() // Он деблокируется сразу

	// Если же меньше, значит нужно найти такую запись в Redis
	requestJson(w, getProductFromRedis(PRODUCT_PAGE_PREFIX+query_page))
}

// Чтобы особо не возится со всякими БД, я просто с FakeApi буду брать данные и их же кэшировать.
func getProduct(query_page string) string {
	// URL для GET-запроса
	url := "https://fakerapi.it/api/v2/books?_quantity=12"

	// Отправляем GET-запрос
	response, _ := http.Get(url)
	defer response.Body.Close()

	// Читаем тело ответа
	body, _ := ioutil.ReadAll(response.Body)
	// Перерабатываем тело ответа в JSON
	var answer map[string]interface{} = make(map[string]interface{})
	_ = json.Unmarshal(body, &answer)
	// Забираем у него ключ DATA и перегоняем в строчку
	bytes, _ := json.Marshal(answer["data"])
	// Закидываем в Redis
	redisConnect.Set(redisCtx, query_page, string(bytes), 0)
	// Выводим ответ на экран
	return string(bytes)
}

// Изначально запрашиваем продукты из Redis
func getProductFromRedis(query_page string) string {
	// Если продукция у нас нашлась, то просто вернём её, в противном случае выполняем getProduct
	val, err := redisConnect.Get(redisCtx, query_page).Result()
	if err != nil {
		// Если в Redis(Cache) не было данных пользуемся стандартной функцией
		return getProduct(query_page)
	}
	return val
}

// Функция возвращает ответ в виде JSON
func requestJson(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(data))
}

func garbageCollector() {
	// Ну тут я конечно пиздец напудрил, как мне кажется
	for {
		// Нууу запускаем каждые 2 часа
		time.Sleep(2 * time.Hour)
		// Запоминаем время
		currentTime := time.Now()
		go garbagePages(currentTime)
		go garbageClients(currentTime)
	}
}

func garbagePages(currentTime time.Time) bool {
	// Я не совсем понял как они работают, эти mutex
	// А точнее вообще
	pageMutex.Lock()         // Он блокируется
	defer pageMutex.Unlock() // Он деблокируется, но только когда функция что-то вернёт
	// Я
	for key, value := range pages {
		sub := currentTime.Sub(value).Minutes()
		if sub > PAGES_TIME_TO_SAVE*PAGES_TIME_TO_SAVE_FACTOR {
			err := redisConnect.Del(redisCtx, key).Err()
			if err != nil {
				fmt.Printf("%s\n", err)
			}
			delete(pages, key)
		}
	}
	return true
}
func garbageClients(currentTime time.Time) bool {
	clientMutex.Lock()         // Он блокируется
	defer clientMutex.Unlock() // Он деблокируется, но только когда функция что-то вернёт
	for key, value := range clients {
		sub := currentTime.Sub(value).Minutes()
		if sub > CLIENTS_TIME_TO_SAVE*CLIENTS_TIME_TO_SAVE_FACTOR {
			err := redisConnect.Del(redisCtx, key).Err()
			if err != nil {
				fmt.Printf("%s\n", err)
			}
			delete(clients, key)
		}
	}
	return true
}
