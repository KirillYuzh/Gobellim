# Политика безопасности

**[English](SECURITY.md) · [Русский](SECURITY.ru.md)**

Gobellim находится между обработчиком приложения и LLM. Это библиотека внутри
процесса приложения, а не sandbox против злонамеренного Go-кода в этом же
процессе.

## Поддерживаемые версии

Gobellim до 1.0. Поддерживается только последний коммит в `main`. Исправления
попадают сначала в `develop`, затем в `main` через release pull request.

## Сообщить об уязвимости

Не открывайте публичный issue. Используйте GitHub Security Advisories:
<https://github.com/KirillYuzh/Gobellim/security/advisories/new>

Укажите затронутый коммит, наблюдение, минимальное воспроизведение и предполагаемое
влияние.

## Модель угроз

Доверяем:

- коду приложения, его конфигурации и зарегистрированным tool handlers;
- `Principal`, созданному после аутентификации;
- источникам policy;
- решению аутентифицированного approver в рамках показанного ему действия.

Не доверяем:

- ответам модели, включая структурированный JSON;
- пользовательскому тексту, документам, результатам поиска и письмам;
- результатам инструментов с внешними данными;
- аргументам tool call до decode и Gate;
- свободному тексту, сгенерированному моделью.

Полная модель и инварианты — в [`DESIGN.md`](./DESIGN.md) §2–§3.

## Что обеспечено конструкцией

- Каждый model-initiated tool call проходит через `gate.go`.
- Неизвестный инструмент, невалидные аргументы, ошибка авторизации и
  ungrounded-аргументы — `Deny`.
- Метки могут только ухудшаться. `Tool.Validate` только отказывает; потолок
  поднимает лишь одобрение вне модели конкретного действия, и оно заменяет
  потолок операции, а не метки.
- Handle, выданный не в этом run, handle в non-`ground` поле и handle из чужого
  входного поля отклоняются как ungrounded.
- Любая операция, кроме `OperationCompute`, требует непустого `Target`: ACL не
  может молча выродиться в RBAC.
- Одобрение привязано ко всей карточке и каноническим аргументам через HMAC и
  digest; изменённая карточка — `ErrApprovalDrift`.
- Апрувер видит маскированную карточку: secret-аргумент — это handle, pii
  замаскирован, а `PendingAction.Args` хранит исполняемые значения на сервере.
- `Out` резолвит handle'ы этого запуска и вычищает остальные `<hN>`, так что
  внутренний плейсхолдер не доходит до клиента.
- Приостановка — серверный объект, а не bearer-токен: `flow.Resume` проверяет
  tenant, expiry, текущую approval-политику, пересобранный digest действия и
  атомарный claim nonce. Ключа, который надо ротировать, нет.
- Отменённый запрос `ExecuteApproved` не расходует nonce.
- Аудит хранит digest и метки, а не prompt text, сырые аргументы, секреты или
  approval nonces.

## Гарантия → тест

Каждая гарантия выше закреплена тестом: рефакторинг, который уберёт проверку,
уронит CI, а не молча ослабит библиотеку.

| Гарантия                                         | Тест                                                                                                         |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| каждый вызов идёт через Gate                     | `TestUngroundedCallDoesNotReachTheAuthorizer`, `TestRBACDenyDoesNotExecute`                                  |
| неизвестный инструмент и плохие аргументы — Deny | `TestUnknownToolIsDenied`, `TestInvalidOutputIsRejected`                                                     |
| handle вне `ground` — Deny                       | `TestHandleIsRejectedOutsideGroundField`                                                                     |
| handle из чужого поля — Deny                     | `TestGroundRejectsHandleFromAnotherField`                                                                    |
| выдуманный handle — Deny                         | `TestFabricatedHandleIsDenied`, `TestUngroundedArgumentIsDenied`                                             |
| `Validate` ничего не повышает                    | `TestUntrustedHandleWithValidatorStillEscalates`, `TestGroundedHandleWithoutValidatorEscalates`              |
| `Send` требует trusted-происхождения             | `TestUntrustedHandleCannotSend`                                                                              |
| любая операция кроме `Compute` требует цель      | `TestNonReadToolRequiresResource`, `TestReadWithoutTargetIsAConfigError`, `TestWriteWithEmptyTargetIsDenied` |
| карточка одобрения маскирована                   | `TestApprovalCardMasksSensitiveArguments`                                                                    |
| `Out` резолвит свои handle'ы и вычищает чужие    | `TestOutputRestoresIssuedHandleAndDropsUnknown`                                                              |
| `ref` — адресуемость, а не конфиденциальность    | `TestRefIsAddressableWithoutBeingSecret`                                                                     |
| `ground` привязан к объявленному полю входа      | `TestGroundSourceMustExistInTheInput`                                                                        |
| PII-детекторы проверяют контрольные цифры        | `TestPIIChecksums`                                                                                           |
| общий ключ требует общего claim store            | `TestSharedApprovalKeyRequiresASharedClaimStore`, `TestWithSealerRequiresWithStore`                          |
| redaction результата fail-closed                 | `TestResultRedactionCoversMapBytesAndString`, `TestSensitivityOnScalarResultIsRejected`, `FuzzRedactResult`  |
| отменённое одобрение не сжигает nonce            | `TestCanceledApprovalDoesNotConsumeTheNonce`                                                                 |
| одобрение одноразово                             | `TestExecuteApprovedIsSingleUse`, `TestParallelResume`                                                       |
| цель одобрения привязана к tenant                | `TestResumeRejectsForeignTenantTarget`                                                                       |
| подделанная карточка отклоняется                 | `TestTamperedCard`                                                                                           |
| `MaxTokens` ограничен                            | `TestRequestCarriesBoundedMaxTokens`                                                                         |
| claim store ограничен                            | `TestMemClaimsIsBounded`                                                                                     |
| один Engine и Task безопасны к гонкам            | `TestTaskRunIsConcurrentSafe` (с `-race`)                                                                    |
| парсер тегов и редактор держат любой ввод        | `FuzzParseTag`, `FuzzDecodeStrict`, `FuzzResolveHandles`, `FuzzRedactResult`                                 |

## Что не покрыто

- Поведение провайдера модели и сетевой путь до него.
- Компрометация процесса приложения или конфигурации.
- Retrieval и data ACL. Приложение обязано отфильтровать документы до передачи
  их в `Task`.
- Корректность permissions, операций инструментов и resource binding, которые
  объявило приложение.
- Exactly-once для внешнего побочного эффекта.
- Межрепличная одноразовость Resume без `Store`.
- Side-channel и все возможные каналы утечки.
