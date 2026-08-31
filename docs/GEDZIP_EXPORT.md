# GEDZIP 7 export

Формат API: `gedzip`. Результат: `.gdz`, MIME `application/vnd.familysearch.gedcom+zip`. Контейнер соответствует разделу GEDZIP в [FamilySearch GEDCOM 7.0](https://gedcom.io/specifications/FamilySearchGEDCOMv7.html#the-familysearch-gedzip-file-format); MIME зарегистрирован в [IANA media types](https://www.iana.org/assignments/media-types/media-types.xhtml).

## Состав архива

- обязательный `gedcom.ged` с тем же активным генеалогическим графом, что и формат `gedcom`;
- originals активных media в состояниях `uploaded`, `processing` и `ready`;
- доступные variants этих media;
- без `manifest.json`, `checksums.sha256`, audit data и внутренних S3 object keys.

Каждая локальная `FILE` или `TRAN` ссылка в `gedcom.ged` имеет ZIP entry с точно таким же case-sensitive именем. Пути строятся только из UUID и безопасного variant kind:

```text
gedcom.ged
media/{mediaID}/original.jpg
media/{mediaID}/variants/preview.jpg
```

Пользовательский `original_filename` используется только как `FILE.TITL` и не влияет на путь. Поэтому `..`, абсолютные пути, обратные слэши, `META-INF` и коллизия с `gedcom.ged` конструктивно невозможны.

## Multimedia mapping

- `MediaAsset` становится `OBJE` record со стабильным `@O{UUID_HEX}@` и `UID`;
- original становится `OBJE.FILE`, MIME — обязательным `FORM`;
- variants становятся `FILE.TRAN` со своим `FORM`;
- `photo` получает `MEDI PHOTO`, `document` — `MEDI ELECTRONIC`;
- caption или original filename становится `FILE.TITL`, description — `OBJE.NOTE`;
- `PersonMedia` и `primary_media_id` становятся deduplicated `INDI.OBJE` pointers;
- приватный media record получает `RESN CONFIDENTIAL`.

Поддерживаемые MIME и расширения совпадают с текущим media pipeline: JPEG (`.jpg`), PNG (`.png`), WebP (`.webp`) и PDF (`.pdf`). Неизвестный MIME или неконсистентная variant metadata отклоняют export как invalid source вместо создания неоднозначного GEDZIP.

## Проверки worker

До загрузки проверяются ссылки metadata, уникальность source и ZIP paths и суммарный размер. После приватного скачивания каждого S3 object повторно вычисляются фактический размер и SHA-256; несовпадение отменяет архив. `gedcom.ged` сжимается Deflate, уже сжатые media записываются без повторного сжатия. Порядок entries и timestamps детерминирован export snapshot.

Общий размер ограничен `EXPORT_MAX_ARCHIVE_BYTES`. Превышение завершает export с `error_code: archive_too_large`. Готовый `.gdz` проходит обычный private S3, checksum, TTL, requester/Owner authorization и audit flow.
