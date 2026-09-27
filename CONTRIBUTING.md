# Contributing

This is a small project, so a short issue or pull request that says what you
saw and what you expected is the most useful thing you can send.

## Getting set up

```sh
make all            # vet, test, and build ./bin/feedme
./bin/feedme serve  # listen on :8080
```

Go 1.25 or newer. There is no vendor directory and no build tag to remember.

## Before you send a change

```sh
make fmt            # gofmt -l -w .
make vet
make test
```

- Keep the surrounding style. Comments here explain *why* a choice was made,
  not what the next line does.
- Add a test for a behaviour change. Extraction fixes are easiest to test with
  a saved page under `testdata/` and a case in the package that reads it.
- Say which page you tested against, if the change is about extraction.

## Reporting a feed that comes back empty

An empty feed is nearly always a selector problem, and `feedme probe` prints
what the extractor found:

```sh
./bin/feedme probe https://example.com/news
```

Paste that output into the issue. It saves a round trip.

## Brand images

`tools/brand.py` regenerates the social preview and the favicons from the mark
the server embeds. Run it with `python3 tools/brand.py` after changing the
logo, and commit the result.

The two screenshots in the README are photographs of a running instance, so they
cannot be drawn. `tools/screenshot.py` takes them through a browserless
service instead; its docstring has the command. Run it after a layout change,
and commit the result.
