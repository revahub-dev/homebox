<div align="center">
  <img src="/docs/src/assets/lilbox.svg" height="200"/>
</div>

<h1 align="center" style="margin-top: -10px;"> HomeBox </h1>
<p align="center" style="width: 100%;">
   <a href="https://homebox.software/en/">Docs</a>
   |
   <a href="https://demo.homebox.software">Demo</a>
   |
   <a href="https://discord.gg/aY4DCkpNA9">Discord</a>
</p>
<p align="center" style="width: 100%;">
    <img src="https://img.shields.io/github/check-runs/sysadminsmedia/homebox/main" alt="Github Checks"/>
    <img src="https://img.shields.io/github/license/sysadminsmedia/homebox"/>
    <img src="https://img.shields.io/github/v/release/sysadminsmedia/homebox?sort=semver&display_name=release"/>
    <img src="https://img.shields.io/weblate/progress/homebox?server=https%3A%2F%2Ftranslate.sysadminsmedia.com"/>
</p>
<p align="center" style="width: 100%;">
    <img src="https://img.shields.io/reddit/subreddit-subscribers/homebox"/>
    <img src="https://img.shields.io/mastodon/follow/110749314839831923?domain=infosec.exchange"/>
    <img src="https://img.shields.io/lemmy/homebox%40lemmy.world?label=lemmy"/>
</p>
<p align="center" style="width: 100%;">
  <a href="https://www.pikapods.com/pods?run=homebox"><img src="https://www.pikapods.com/static/run-button.svg"/></a>
</p>

## What is HomeBox

HomeBox is the inventory and organization system built for the Home User! With a focus on simplicity and ease of use, Homebox is the perfect solution for your home inventory, organization, and management needs. While developing this project, We've tried to keep the following principles in mind:

- 🧘 _Simple but Expandable_ - Homebox is designed to be simple and easy to use. No complicated setup or configuration required. But expandable to whatever level of infrastructure you want to put into it.
- 🚀 _Blazingly Fast_ - Homebox is written in Go, which makes it extremely fast and requires minimal resources to deploy. In general, idle memory usage is less than 50MB for the whole container.
- 📦 _Portable_ - Homebox is designed to be portable and run on anywhere. We use SQLite and an embedded Web UI to make it easy to deploy, use, and backup.

### Key Features
- 📇 Rich Organization - Organize your items into categories, locations, and tags. You can also create custom fields to store additional information about your items.
- 🔍 Powerful Search - Quickly find items in your inventory using the powerful search feature.
- 📸 Image Upload - Upload images of your items to make it easy to identify them.
- 📄 Document and Warranty Tracking - Keep track of important documents and warranties for your items.
- 💰 Purchase & Maintenance Tracking - Track purchase dates, prices, and maintenance schedules for your items.
- 📱 Responsive Design - Homebox is designed to work on any device, including desktops, tablets, and smartphones.

## Screenshots
![Login Screen](.github/screenshots/1.png)
![Dashboard](.github/screenshots/2.png)
![Item View](.github/screenshots/3.png)
![Create Item](.github/screenshots/9.png)
![Search](.github/screenshots/8.png)

You can also try the demo instances of Homebox:
- [Demo](https://demo.homebox.software)
- [Nightly](https://nightly.homebox.software)

## Quick Start

[Configuration & Docker Compose](https://homebox.software/en/quick-start/)

```bash
# Save pepper to local file
openssl rand -base64 48 > hbox.pepper
chmod 400 hbox.pepper
# If using the rootless or hardened image, ensure data 
# folder has correct permissions
mkdir -p /path/to/data/folder
chown 65532:65532 -R /path/to/data/folder
docker run -d \
  --name homebox \
  --restart unless-stopped \
  --publish 3100:7745 \
  --env TZ=Europe/Bucharest \
  --env HBOX_AUTH_API_KEY_PEPPER=$(cat hbox.pepper) \
  --volume /path/to/data/folder/:/data \
  ghcr.io/sysadminsmedia/homebox:latest
# ghcr.io/sysadminsmedia/homebox:latest-rootless
# ghcr.io/sysadminsmedia/homebox:latest-hardened
```

<!-- CONTRIBUTING -->

## Fork: photo-first capture (Snap) and AI setup

This fork adds **Snap** (`/x/snap`, the camera button): take photos with the live camera (tap for one, hold for a
burst) or pick several photos from the phone gallery, choose *One item* or *Shelf or drawer*, and the AI lists what
it sees. Each detection becomes a card with a crop of the photo: tap **Add**, swipe right to add (or merge, when it
looks like something you already own), swipe left to ignore, or edit it first. **Accept all ready** adds every
high-confidence, non-duplicate card at once, and every decision has an **Undo**. Manual forms are still there
(**Enter manually**). Nothing is added to your inventory until you accept it.

Snap works with **any OpenAI-compatible chat-completions API that accepts images**: cloud (OpenAI and compatible
providers) or a local server (Ollama, LM Studio, llama.cpp server). Switching is configuration only; no rebuild.

| Variable | Default | Meaning |
|---|---|---|
| `HBOX_AI_BASE_URL` | – | API base URL including `/v1` |
| `HBOX_AI_MODEL` | – | model name (required; Snap is off until base URL and model are set) |
| `HBOX_AI_VISION_MODEL` | `HBOX_AI_MODEL` | model for photos, if different from the text model |
| `HBOX_AI_API_KEY` | – | bearer key; leave empty for local servers |
| `HBOX_AI_RESPONSE_FORMAT` | `json_object` | `json_object`, `json_schema` (LM Studio) or `none`; a server that answers HTTP 400 to `response_format` is retried without it automatically |
| `HBOX_AI_TIMEOUT_SEC` | `120` | per-request timeout; raise it for slow local CPU models |
| `HBOX_CAPTURE_WORKERS` | `1` | photos analysed at once (keep `1` for a single local GPU) |
| `HBOX_CAPTURE_MAX_PHOTOS_PER_DAY` | `300` | per-collection cap (`0` = unlimited), a guard against runaway cloud cost |
| `HBOX_CAPTURE_MAX_PHOTO_MB` | `8` | largest accepted upload (the app shrinks photos to ~1600 px before uploading) |
| `HBOX_CAPTURE_RETENTION_DAYS` | `7` | unreviewed photos and crops are deleted after this many days |
| `HBOX_CAPTURE_LANGUAGE` | – | optional language code (`ja`, `en`) for generic item descriptions; text printed on packaging is always kept as written |

The same `HBOX_AI_*` endpoint also powers "What can I build?" and sale-listing drafts. If only the older
`HBOX_ORDER_IMPORT_LLM_*` variables are set, they are used as a fallback.

**Examples** (use any vision-capable model; the model names below are examples):

```bash
# OpenAI (cloud). Photos are sent to the provider.
HBOX_AI_BASE_URL=https://api.openai.com/v1
HBOX_AI_API_KEY=sk-...
HBOX_AI_MODEL=gpt-4o-mini

# Ollama on the host (photos stay on your network). From Docker use host.docker.internal.
HBOX_AI_BASE_URL=http://host.docker.internal:11434/v1
HBOX_AI_MODEL=qwen2.5vl:7b          # `ollama pull qwen2.5vl:7b` first
HBOX_AI_TIMEOUT_SEC=300

# LM Studio on the host (load a vision model and start its local server)
HBOX_AI_BASE_URL=http://host.docker.internal:1234/v1
HBOX_AI_MODEL=<model id shown in LM Studio>
HBOX_AI_RESPONSE_FORMAT=json_schema
```

With Docker Compose, put these in a `docker-compose.override.yml` next to `docker-compose.yml` (it is merged
automatically):

```yaml
services:
  homebox:
    environment:
      - HBOX_AI_BASE_URL=http://host.docker.internal:11434/v1
      - HBOX_AI_MODEL=qwen2.5vl:7b
      - HBOX_AI_TIMEOUT_SEC=300
```

Notes:

- The Snap header shows a **Local AI** or **Cloud AI** badge. It is guessed from the host name (localhost, private
  IPs, `.local` and single-word Docker service names count as local) and is only a hint.
- Small local models are slower, name things less precisely and place boxes less accurately than large cloud
  models. Everything the model returns is validated and clamped, and uncertain results are shown as *Check* or
  *Low confidence* instead of being added automatically.
- **Live camera needs HTTPS** (a browser rule). Over plain `http://<lan-ip>:3100` the phone still works through
  **Take photo** (opens the native camera app) and **From gallery**. For the live camera, serve HomeBox over HTTPS
  (for example with a reverse proxy such as Caddy, or Tailscale Serve).
- Ollama on Windows listens on `127.0.0.1` by default. If the container cannot reach it, set the environment
  variable `OLLAMA_HOST=0.0.0.0` for Ollama and restart it.

## Contributing

Contributions are what make the open source community such an amazing place to learn, inspire, and create. Any contributions you make are **greatly appreciated**.

To get started with code based contributions, please see our [contributing guide](https://homebox.software/en/contribute/).

If you are not a coder and can't help translate, you can still contribute financially. Financial contributions help us maintain the project and keep demos running.

## Help us Translate
We want to make sure that Homebox is available in as many languages as possible. If you are interested in helping us translate Homebox, please help us via our [Weblate instance](https://translate.sysadminsmedia.com/projects/homebox/).

[![Translation status](https://translate.sysadminsmedia.com/widget/homebox/multi-auto.svg)](https://translate.sysadminsmedia.com/engage/homebox/)

## Credits
- Original project by [@hay-kot](https://github.com/hay-kot)
- Logo by [@lakotelman](https://github.com/lakotelman)

### Contributors
<a href="https://github.com/sysadminsmedia/homebox/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=sysadminsmedia/homebox" />
</a>
