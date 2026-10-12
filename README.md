# Uma

A menu bar app that shows the websites of an [Umami](https://umami.is) account
and their visitors, built with [MyGo](https://github.com/egoist/mygo).

## Requirements

- Go 1.27 or newer
- macOS, Windows, or Linux (a desktop session is needed to run the app)

## Run locally

```sh
go tool mygo dev
```

This builds a development bundle, launches it, and rebuilds and relaunches it
whenever the Go code, `mygo.json` or the icon changes. Press `q` in the
terminal to quit.

Check that the environment is ready first, if you like:

```sh
go tool mygo doctor
```

## Build a production app

```sh
go tool mygo build
```

The result goes to `build/<platform>/` — a signed `.app` and a `.dmg` on
macOS, an executable elsewhere. Build for another platform with `-platform`,
for example:

```sh
go tool mygo build -platform darwin/universal,linux/amd64,windows/amd64
```

## Configure the app

On first launch, open the panel from the menu bar icon and set:

- **API endpoint** — defaults to Umami Cloud, `https://api.umami.is/v1`. For a
  self-hosted server, give its address, e.g. `https://analytics.example.com/api`
  (`/api` is added for you if you leave it off)
- **API key** — an Umami API key, from *Settings → API keys*

On macOS the key is kept in the login keychain; elsewhere it is stored in the
app's user data directory.
