# Host nginx configuration for nexspence.com

These files configure the **host** nginx that terminates TLS and reverse-proxies
to the website container published by `.github/workflows/deploy-website.yml`.
They are applied manually on the droplet and are intentionally kept outside
`website/`, which is the Docker build context (`COPY .` would otherwise publish
them as static files).

| File | Destination on the host |
| --- | --- |
| `nginx.conf` | `/etc/nginx/nginx.conf` |
| `nexspence_com` | `/etc/nginx/sites-available/nexspence_com` |
| `nexspence_online` | `/etc/nginx/sites-available/nexspence_online` (301 → `.com`) |

Apply with:

```bash
sudo cp nginx.conf /etc/nginx/nginx.conf
sudo cp nexspence_com nexspence_online /etc/nginx/sites-available/
sudo ln -sf /etc/nginx/sites-available/nexspence_com /etc/nginx/sites-enabled/
sudo ln -sf /etc/nginx/sites-available/nexspence_online /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

The container's own server block lives in `website/nginx.conf` — do not confuse
the two.
