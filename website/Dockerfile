FROM nginx:alpine

# Links the GHCR package to this repository (auto-linked repository).
LABEL org.opencontainers.image.source=https://github.com/nexspence/nexspence

COPY . /usr/share/nginx/html/

# Custom server config.
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Run as the unprivileged nginx user (uid 101). That means binding a port above
# 1024, so the server listens on 8080 — see nginx.conf. The pid file and cache
# directories have to be writable by that user before dropping privileges.
RUN touch /var/run/nginx.pid && \
    chown -R nginx:nginx /var/cache/nginx /var/run/nginx.pid /usr/share/nginx/html

USER nginx

EXPOSE 8080
