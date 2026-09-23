# Règles — module `backup` (code `BKP`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module orchestre une sauvegarde. Il ne dumpe pas — c'est `engine` —, il n'écrit pas — c'est
`store` —, il n'assemble pas le flux — c'est `pipeline`. Il décide **dans quel ordre**, **si on peut
commencer**, et **ce qu'on fait quand ça casse**.

## Le déroulement d'un job

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-01 | Un seul job est actif **par base** à tout instant, tenu par un **fichier de verrou** ; une seconde demande est **refusée** en nommant le job en cours, **jamais mise en file**. Un verrou laissé par un processus qui n'existe plus — ou illisible — **ne bloque pas** : il est repris. Le fichier dit qui le tient, en clair, pour qu'on le débloque à la main à 3 h du matin. | `E-051`, § 5.3 `F3.1`, `N-5` | `backup/lock_test.go › TestBKP01ASecondJobOnTheSameDatabaseIsRefused`, `› TestBKP01ALockWhoseProcessIsGoneIsTakenOver`, `› TestBKP01ALockFileThatCannotBeReadIsTakenOver`, `› TestBKP01TheLockSaysWhoHoldsIt`, `backup/service_test.go › TestASecondJobOnTheSameDatabaseIsRefusedByTheUseCase` |
| BKP-02 | Les **trois modes** de tampon existent et se déclarent **par base**. Une décision ne rend **jamais** `auto` : `auto` est une question, un job a besoin de la réponse — et de la **raison**, qui part au manifeste. | `E-029`, § 4.5 | `backup/staging_test.go › TestBKP02TheThreeModesAreSelectablePerDatabase` |
| BKP-03 | Le mode `stage` est **imposé** dans les trois cas du § 4.5 — format répertoire, plus d'une destination, vérification structurelle sans egress — **par-dessus** un `stream` explicite, et la décision **dit lequel** l'a imposé. Ce sont des situations où le flux direct ne marche pas, pas des préférences. | `E-030`, § 4.5 | `backup/staging_test.go › TestBKP03StageIsImposedByTheThreeCasesOfTheSpecification` |
| BKP-04 | En `auto`, l'arbitrage se fait sur l'**espace libre** avec la marge **× 1,5** du § 4.5, et le mode **effectivement appliqué** est enregistré. Deux exécutions de la même base peuvent légitimement différer ; un manifeste qui ne dit pas laquelle est illisible. | `E-053`, `N-1`, `N-4` | `backup/staging_test.go › TestBKP04AutoChoosesOnTheFreeSpaceAndSaysWhatItChose` |
| BKP-05 | L'espace est vérifié **avant** de commencer. La taille attendue vient de la **dernière sauvegarde réussie**, à défaut de la **taille de la base** rapportée par la sonde, divisée par **8** (ADR-0016 : la recette a mesuré 7,8 % et 3,9 % là où koffr supposait 25 %). Un job qui remplirait le disque **bascule en `stream`**, ou est **refusé** quand `stage` est imposé et qu'il n'y a nulle part où se rabattre. | `E-061`, § 5.3 `F3.10`, `N-12` | `backup/staging_test.go › TestBKP05TheExpectedSizeComesFromTheLastBackupFirst`, `› TestBKP05AJobThatWouldFillTheDiskFallsBackOrIsRefused`, `› TestBKP05AnUnknownFreeSpaceDoesNotPassSilently` |
| BKP-06 | Les **sept étapes** de `E-024` s'enchaînent **dans cet ordre**, et le résultat les porte toutes les sept, faites ou non. Une étape qui échoue **arrête** le job et celles d'après ne sont **pas** déclarées faites. Vérification et manifeste sont **absentes et déclarées telles** jusqu'au lot 3 : un job qui prétendrait avoir vérifié est exactement ce que `P3` interdit. | `E-024`, § 4.1, § 2 `P3` | `backup/service_test.go › TestBKP06TheSevenStepsHappenInTheOrderOfTheSpecification`, `› TestBKP06AFailedStepStopsTheJob` |

## La chaîne en flux

Ces trois règles portent sur ce que `internal/pipeline` garantit. Elles vivent ici parce qu'un
adaptateur n'a pas de `rules.md` (ADR-0010) et qu'`ARCHITECTURE.md` ne déclare pas de code `PIP`
(`N-9`).

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-07 | Le dump brut n'est **jamais matérialisé**, ni en mémoire ni sur disque : dump, compression, chiffrement et empreintes sont chaînés **en une seule passe**. Ce qui est retenu en mémoire reste borné quelle que soit la taille de la base. | `E-025`, § 4.5 `F3.3` | `pipeline/pipeline_test.go › TestBKP07TheStreamIsNeverHeldWhole` |
| BKP-08 | **Deux** empreintes sont calculées au vol : `sha256_raw` sur le dump **avant** compression, `sha256_stored` sur ce qui est réellement écrit. Seule la seconde se vérifie sans déchiffrer, et le lot 3 en dépend. | `E-024`, `N-7` | `pipeline/pipeline_test.go › TestBKP08BothChecksumsAreComputedOnTheWay` |
| BKP-09 | Une erreur **au milieu** du flux — dump interrompu, disque plein, destinataire refusé — remonte **typée** et ne produit **jamais** un résultat qui ressemble à un succès. Un octet écrit n'est pas une sauvegarde. | `E-024`, § 2 `P3` | `pipeline/pipeline_test.go › TestBKP09AFailureMidStreamIsNeverASuccess` |

## La trace d'un job

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-20 | Les **sept étapes** de `E-024` sont journalisées **dans l'ordre**, chacune avec ce qu'elle a produit — outil et version, mode de tampon, pile et empreintes, chemin et destinations. Une étape qui échoue est journalisée **avec son erreur**, et **rien** n'est journalisé après : une trace qui montre sept étapes pour un job mort à la deuxième est pire qu'aucune trace. Les deux étapes non implémentées se déclarent telles. | `A-18`, `E-024`, `E-026`, `N-1` | `backup/journal_test.go › TestBKP20TheSevenStepsAreJournalledInOrder`, `› TestBKP20AFailedStepIsJournalledAndStopsTheTrace`, `cli/endtoend_test.go › TestTheLogFileCarriesWhatABackupDid` |
| BKP-21 | Le journal ne porte que des faits **énumérés**. Un identifiant de connexion n'en est pas un, et en ajouter un est une **décision**, pas une édition. Le domaine ne reçoit jamais la cible ni le mot de passe : c'est ce qui l'empêche de les divulguer, et la liste est ce qui le maintient. | `E-115`, `A-18` | `backup/journal_test.go › TestBKP21TheJournalOnlyCarriesFactsThatWereDeclared` |

## L'identité d'une archive

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-19 | Un job et son archive portent un **ULID** de 26 caractères. Deux identifiants produits dans la même milliseconde sont **différents et ordonnés** — le catalogue du lot 3 listera les archives par identifiant. L'aléa est **cryptographique** et rien ne panique : jamais `ulid.Make`, qui tire de `math/rand` et passe par `MustNew`. | ADR-0006, `A-14`, `N-2` | `backup/id_test.go › TestBKP19AnIdentifierIsARealULID`, `› TestBKP19IdentifiersMadeTogetherStaySorted`, `› TestNothingCallsTheConvenientULIDHelpers` |

## Le manifeste

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-23 | Le manifeste est déposé **à côté de l'archive, sur chaque destination**, sous `<archive>.json`, et il est écrit **en dernier** — après l'archive, dont il décrit la taille et l'empreinte, et après la vérification, dont il porte l'état. Un manifeste qui ne peut pas être écrit **fait échouer le job** : une archive sans son manifeste est une archive que personne ne peut inventorier. Le domaine demande des **octets** et ne sait pas ce qu'il y a dedans — la forme appartient à `catalog`, et `AR-03` sépare les deux. | `E-057`, `E-024`, `N-4` | `backup/service_test.go › TestTheManifestIsDepositedBesideTheArchiveOnEveryDestination`, `› TestTheManifestIsWrittenAfterTheArchive`, `› TestAManifestThatCannotBeWrittenFailsTheJob` |

## Le tampon

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-17 | Un job **purge les tampons abandonnés** avant de commencer — ceux dont le processus propriétaire n'existe plus. `internal/state` sait purger ce répertoire (`E-026`) mais une sauvegarde n'ouvre jamais l'état, donc rien ne les collectait : un job tué laissait son tampon pour toujours et le disque se remplissait, ce que `E-061` existe pour éviter. Le tampon d'un processus **vivant** n'est **jamais** touché : le verrou est par base, donc deux jobs coexistent. | `A-16`, `E-026`, `E-061`, `N-4` | `backup/staging_file_test.go › TestBKP17AJobPurgesWhatAKilledJobLeftBehind`, `› TestBKP17ALivingJobsStagingFileIsNotTouched`, `cli/endtoend_test.go › TestAKilledJobsBufferIsGoneAfterTheNextBackup` |
| BKP-18 | Un job **ne laisse aucun tampon**, qu'il réussisse ou qu'il échoue. Seul un processus tué peut en laisser un, et c'est `BKP-17` qui le ramasse. | `A-16`, § 4.5 | `backup/staging_file_test.go › TestBKP18AFailedJobLeavesNoBuffer`, `› TestBKP18ASuccessfulJobLeavesNoBuffer` |

## Où l'archive est écrite

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-10 | Le chemin d'une archive est **déterministe et lisible par un humain** : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<extension>`. Il se reconstruit **sans la base locale**, pour qu'un dépôt reste exploitable si l'agent disparaît — et il ne contient jamais d'identifiant de connexion. **L'extension dit ce que le fichier *est*, dans l'ordre où on le défait** (`pgc.zst.age`), et se construit à partir de la pile que le pipeline a réellement appliquée, pas d'une chaîne écrite à la main. | `E-070`, § 5.5 `F5.5`, `A-13`, `N-3` | `backup/path_test.go › TestBKP10TheArchivePathIsDeterministicAndReadable`, `› TestBKP10TheExtensionCarriesTheStackThatWasApplied`, `› TestBKP10TheExtensionReadsInTheOrderYouUndoIt` |
| BKP-11 | Une écriture interrompue **ne laisse pas d'archive partielle visible** : on écrit à côté, puis on renomme. Un fichier qui porte le nom d'une archive est une archive entière. | `E-066`, § 2 `P3` | `store/storetest › an interrupted write leaves nothing visible` |
| BKP-12 | Tester l'accès à une destination **dit pourquoi** il échoue — répertoire absent, droits, disque plein — et n'écrit rien de durable. | `E-066`, § 5.5 `F5.1` | `store/storetest › checking access` |

## Le dump

Ces règles portent sur ce que `internal/engine` garantit en lançant l'outil. Elles vivent ici pour
la raison de `N-9` : un adaptateur n'a pas de `rules.md`.

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-13 | Le dump sort **sur un flux**, jamais dans un fichier intermédiaire : PostgreSQL en `-Fc` avec `--no-owner --no-privileges` **et `--compress=0`** — `pg_dump` compresse ce format lui-même, et le laisser faire donnerait à zstd un flux déjà compressé et fausserait l'arithmétique du § 4.5 —, MySQL et MariaDB avec `--single-transaction`, routines, déclencheurs et événements. Le mot de passe voyage par **l'environnement**, jamais sur la ligne de commande. | `E-054`, `E-056`, `E-115`, `N-3` | `engine/dump_test.go › TestADumpOfPostgreSQLIsAnArchivePgRestoreReads`, `› TestThePostgreSQLDumpCarriesTheOptionsOfTheSpecification`, `› TestTheMySQLFamilyDumpIsTransactionalAndComplete` |
| BKP-14 | **Fermer le dump attend le sous-process** et remonte ce qu'il a dit : un dump dont le process a échoué produit une archive parfaitement formée de rien, et seul le code de retour les distingue. Un dump **abandonné en cours de lecture est arrêté**, pas laissé tourner. C'est aussi ce qui ferme la connexion à la base **avant** que le moindre envoi ne commence. | `E-055`, § 2 `P3` | `engine/dump_test.go › TestADumpThatFailsIsAFailure`, `› TestClosingTheDumpEndsTheConnection`, `› TestClosingAHalfReadDumpStopsIt` |
| BKP-15 | Le format répertoire `-Fd` est **refusé dans la stratégie `exec`**, avec un message qui nomme le conteneur et la sortie — koffr ne sait pas relire un répertoire rempli dans un conteneur comme une archive. Hors conteneur, il **n'est pas un flux** non plus : il s'écrit dans un répertoire de tampon, ce qui est la raison pour laquelle le CDC lui impose le mode `stage`. | `E-054`, ADR-0015, § 5.3 `F3.5` | `engine/dump_test.go › TestTheDirectoryFormatIsRefusedInTheExecStrategy`, `› TestTheDirectoryFormatSaysItNeedsStaging` |
| BKP-16 | Quand une base déclare la stratégie `exec`, **le dump lui-même** passe dans son conteneur, avec le client de son image, et le flux en ressort. La résolution seule ne suffit pas : c'est ce qu'ADR-0015 exige pour un parc mixte MySQL / MariaDB. | `E-046`, ADR-0015 | `engine/dump_test.go › TestAMariaDBIsDumpedByTheClientOfItsOwnContainer`, `› TestAPostgreSQLIsDumpedInsideItsOwnContainer` |

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Niveau de compression | `zstd:3` | `N-4` (hypothèse de `Q-08`) | `pipeline/pipeline_test.go` |
| Marge d'espace disque | × 1,5 | § 4.5, `N-4` | `backup/staging_test.go` |
| Compression supposée sans historique | **÷ 8** | ADR-0016, mesuré en recette (7,8 % et 3,9 %) | `backup/staging_test.go` |
| Répertoire des verrous | `<état>/locks/` | `N-5` | `backup/lock_test.go` |
| Répertoire de tampon | `<état>/tmp/` | `E-026`, § 4.5 | `backup/service_test.go` |
| Nom d'un tampon | `staging-<pid>-<job>.koffr` | `N-4` | `backup/staging_file_test.go` |
| Extension d'une archive | `<format>.zst.age` | `A-13`, `N-3` | `backup/path_test.go` |
| Identifiant | ULID, 26 caractères | ADR-0006, `N-2` | `backup/id_test.go` |
