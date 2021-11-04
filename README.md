# Yggdrasil Extras

Generate an iOS framework with:

```
gomobile bind -target ios -tags mobile -o Yggdrasil.xcframework \
  github.com/yggdrasil-network/yggdrasil-extras/src/mobile \
  github.com/yggdrasil-network/yggdrasil-go/src/config
```

Generate an Android AAR bundle with:

```
gomobile bind -target android -tags mobile -o yggdrasil.aar \
  github.com/yggdrasil-network/yggdrasil-extras/src/mobile \
  github.com/yggdrasil-network/yggdrasil-go/src/config
```
