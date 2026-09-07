# rssnip

- RSSを切り取るCLIツール
    - 他のツールと連携してクローリングとかをしたい
- 期間指定
- フィルタ機能などを設ける？
    - jqにどれくらい寄せるか？
    - 最低限のフィルタリング引数指定しつつも、細かいフィルタはjqでやるか
- jsonl形式で出力する
    - 以下の出力形式は仮

```sh
$ rssnip --since 2024-01-01 --until 2024-01-31 --url https://example.com/rss.xml
{"title": "Example Title 1", "link": "https://example.com/article1", "pubDate": "2024-01-05"}
{"title": "Example Title 2", "link": "https://example.com/article2", "pubDate": "2024-01-15"}

```

## --jq オプション

```sh
$ rssnip --since 2024-01-01 --until 2024-01-31 --url https://example.com/rss.xml -r --jq '.link'
https://example.com/article1
https://example.com/article2
```

## サポートするフィード

基本的なフィード形式はサポートしたい。

- RSS 2.0
- Atom 1.0
- RDF 1.0
- JSON Feed 1.0
- RSS 1.0

統一的なデータ構造で出力するようにしたい。JSON Feedに正規化する？

## 設計案
- sinceやuntilなどの期間指定は、RFC3339形式で指定する
    - gitのような指定もできると嬉しいが
    - 期間指定のデフォルトは、過去1週間くらいにする?
    - ページングも可能ならたどりたい
- 複数RSSフィードを指定できるようにする?
- blogのURLからフィードをディスカバリするようにする？
- JSONかJSONLか
    - JSONLの方が扱いやすい気はするが、各記事のURL以外のmetaデータも出したくなるかも？
    - オプションかな
- conditional getとかはとりあえず考えなくて良いかね
- サブコマンドは無くて良い
    - 後付は必要になったらする
- ライブラリ類
    - gojqがあるのは強い
    - RSSのパースはgofeedを使うのが良い？
- 期間指定はコマンドラインflagをもたせるけど、それ以外はjqでやるという設計方針で良い？
    - JSON Feedに正規化して出力することで、jqでのフィルタリングが容易になるはずだが、それで大丈夫か？
