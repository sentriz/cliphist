# Changelog

## [0.5.0](https://www.github.com/sentriz/cliphist/compare/v0.4.0...v0.5.0) (2023-12-12)


### Features

* accept multiple input lines for `cliphist delete` ([fbc54c0](https://www.github.com/sentriz/cliphist/commit/fbc54c0fe4c930cd24eb3c70134c3c3a1b3dcd2d)), closes [#63](https://www.github.com/sentriz/cliphist/issues/63)
* add support for bmp images ([3c9a7b8](https://www.github.com/sentriz/cliphist/commit/3c9a7b889e4eeed301e71d67ed37246cc9097e63)), closes [#46](https://www.github.com/sentriz/cliphist/issues/46)
* add version command ([3e91638](https://www.github.com/sentriz/cliphist/commit/3e91638630fc54f815ae025fa77e2472a770e91a))
* allow passing an id directly to decode ([a2ead3d](https://www.github.com/sentriz/cliphist/commit/a2ead3d83dd67ceb7189246ce428a21c559a449e))
* clear last entry if we get `clear` from CLIPBOARD_STATE ([5764b03](https://www.github.com/sentriz/cliphist/commit/5764b0345432b07eca49914e603a6fd2d1431a73)), closes [#58](https://www.github.com/sentriz/cliphist/issues/58)
* **contrib:** add rofi script with support for displaying images ([#70](https://www.github.com/sentriz/cliphist/issues/70)) ([3da9a90](https://www.github.com/sentriz/cliphist/commit/3da9a90de9828018149bb11befd3c1d7f2ed44d5))
* **contrib:** speed up cliphist-rofi-img script and make compatible with 0.4 ([95c1936](https://www.github.com/sentriz/cliphist/commit/95c193604fce7c5ec094ff9bf1c62cc6f5395750)), closes [#71](https://www.github.com/sentriz/cliphist/issues/71)
* don't store items >5MB ([594ca54](https://www.github.com/sentriz/cliphist/commit/594ca54b6c9d3363f7c59b95a40832d71bf8c5e5)), closes [#68](https://www.github.com/sentriz/cliphist/issues/68)
* show image size and dimensions in preview ([dd3c5f0](https://www.github.com/sentriz/cliphist/commit/dd3c5f0248065d8f36c48445b3e68ffb6000ff86))

## [0.4.0](https://www.github.com/sentriz/cliphist/compare/v0.3.1...v0.4.0) (2023-03-08)


### Features

* add -max-items and -max-dedupe-search command line args ([df85b70](https://www.github.com/sentriz/cliphist/commit/df85b70a9033cb400ca8758192eb901f21718f04)), closes [#35](https://www.github.com/sentriz/cliphist/issues/35)
* add "wipe" command ([c2e08d9](https://www.github.com/sentriz/cliphist/commit/c2e08d998c0724da37d179c8aa2356913241c35b))
* use tab as the id separator ([bf792ca](https://www.github.com/sentriz/cliphist/commit/bf792cab257db65be5d1287723353d380e9ecccc))

### [0.3.1](https://www.github.com/sentriz/cliphist/compare/v0.3.0...v0.3.1) (2022-03-12)


### Bug Fixes

* read write std io before opening db ([4a54efe](https://www.github.com/sentriz/cliphist/commit/4a54efe6fd027e2bef933d3f2d7270badae5d919)), closes [#26](https://www.github.com/sentriz/cliphist/issues/26)

## [0.3.0](https://www.github.com/sentriz/cliphist/compare/v0.2.0...v0.3.0) (2021-10-26)


### ⚠ BREAKING CHANGES

* set delete commands to db `delete` / `delete-query`

### Features

* don't ignore small inputs ([7cd236c](https://www.github.com/sentriz/cliphist/commit/7cd236ceeeab83bbd8c26baad230cee60807ded1)), closes [#17](https://www.github.com/sentriz/cliphist/issues/17)


### Code Refactoring

* set delete commands to db `delete` / `delete-query` ([4e8c459](https://www.github.com/sentriz/cliphist/commit/4e8c45991456f3e69d7db3c0a5f799129acbaa71))

## [0.2.0](https://www.github.com/sentriz/cliphist/compare/v0.1.0...v0.2.0) (2021-10-24)


### Features

* add delete-stdin command ([db1ebf1](https://www.github.com/sentriz/cliphist/commit/db1ebf1e937c22d7dfbd51dd17854f9f282840e3)), closes [#18](https://www.github.com/sentriz/cliphist/issues/18)

## 0.1.0 (2021-09-17)


### Features

* add LICENSE ([d6389f9](https://www.github.com/sentriz/cliphist/commit/d6389f951b3e70b52ac116d1015de5fed41ddba0))
* **ci:** setup release please ([6bd3aeb](https://www.github.com/sentriz/cliphist/commit/6bd3aeb4b5a8473097db788a341b002368821aee))


### Bug Fixes

* **ci:** disable errcheck ([79c5d6c](https://www.github.com/sentriz/cliphist/commit/79c5d6cfdf321a93e2cbd2f2645672c7335a7d1e))
