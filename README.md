# go_cart
For Egor and BETTERLIFE outside the MARS

Для запуска необходимо:
1. Скачать nginx и заменить nginx.conf файл.
2. В nginx.conf location / имеет 2 состояния
2.1. Ссылка на папку по относительному пути. Значит данные исходники нужно будет положить рядышком. Папка nginx и папка go_cart должны быть на одном уровне.
2.2. Ссылка на localhost:3000. Это базовый сервер react. Нужно будет скачать его. Выполнить команды "npm install" и "npm start". Тогда сервер запустится, естественно должен быть Node.js.
2.2.1. Ссылка на React-исходники https://github.com/321template123/react_cart.git
3. Redis ещё нужно будет развернуть и в main.go изменить переменные подключения.
