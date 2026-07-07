FROM nginx:alpine

# Привязка GHCR-пакета к этому репозиторию (авто-linked repository)
LABEL org.opencontainers.image.source=https://github.com/nexspence/nexspence

COPY . /usr/share/nginx/html/

# Кастомный конфиг
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80