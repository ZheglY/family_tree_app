# GEDCOM 7 export

Формат API: `gedcom`. Результат: `.ged`, MIME `text/vnd.familysearch.gedcom`. Реализация ориентирована на [FamilySearch GEDCOM 7.0](https://gedcom.io/specifications/FamilySearchGEDCOMv7.html); MIME зарегистрирован в [IANA media types](https://www.iana.org/assignments/media-types/media-types.xhtml).

## Граница данных

Экспорт является переносимым представлением генеалогического графа, а не полной резервной копией. В него входят только активные `Person`, `PersonName`, `ParentChildRelation`, `FamilyUnion` и `UnionMember`. Soft-deleted данные, пользователи и memberships, audit log, media metadata, S3 object keys и бинарные файлы исключаются. Для полного восстановления используется `zip_backup`; для переносимого GEDCOM вместе с файлами используется `gedzip`.

## Mapping

| Family Tree | GEDCOM 7 |
|---|---|
| `Person.id` | стабильный `@I{UUID_HEX}@` и `UID` |
| все имена активной персоны | `INDI.NAME`, preferred первым; structured pieces в `NPFX/GIVN/SURN/NSFX` |
| `sex` | `SEX M/F/U` |
| `life_status=deceased` | `DEAT Y` |
| `biography`, `notes` | отдельные `NOTE`, многострочный текст через `CONT` |
| `privacy_level=tree_members` | `RESN CONFIDENTIAL` |
| два участника союза | `FAM.HUSB/WIFE` и взаимные `INDI.FAMS` |
| `marriage`, `engagement` | `MARR Y`, `ENGA Y` |
| `civil_union`, `partnership`, `unknown` | стандартный generic `FAM.EVEN` + `TYPE` |
| parent-child relation | `FAM.CHIL`, взаимный `INDI.FAMC` |
| biological/adoptive/foster | `PEDI BIRTH/ADOPTED/FOSTER` |
| guardian/step/unknown | `PEDI OTHER` + `PHRASE` |
| confirmed/disputed confidence | `STAT PROVEN/CHALLENGED` |
| relation note и нестандартный confidence | subordinate `NOTE` |

Стандарт допускает в `FAM` только два partner pointers. Союз с 3–10 участниками поэтому детерминированно преобразуется в несколько `FAM`: первый участник связывается с каждым последующим. Parent-child relation не приписывает родительство партнёру автоматически: существующая union-family переиспользуется только при точном совпадении набора родителей, иначе создаётся отдельная синтетическая `FAM`.

## Гарантии writer

- UTF-8 с начальным BOM и едиными CRLF line endings;
- `HEAD.GEDC.VERS 7.0` и завершающий `TRLR`;
- cross-reference identifiers содержат только допустимые uppercase letters, digits и `_`;
- leading `@` в строковом payload удваивается, pointer payload остаётся pointer;
- запрещённые control characters удаляются, переносы кодируются через `CONT`;
- records, имена, союзы и связи имеют детерминированный порядок;
- каждый `FAM.HUSB/WIFE/CHIL` имеет взаимный `INDI.FAMS/FAMC`;
- результат, checksum и MIME проходят обычный private S3/TTL/audit pipeline.

Размер готового файла ограничен `EXPORT_MAX_ARCHIVE_BYTES`. Превышение не повторяется worker-ом и завершает export с `error_code: result_too_large`.
