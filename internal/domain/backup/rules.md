# Règles — module `backup` (code `BKP`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module orchestre une sauvegarde. Il ne dumpe pas — c'est `engine` —, il n'écrit pas — c'est
`store` —, il n'assemble pas le flux — c'est `pipeline`. Il décide **dans quel ordre**, **si on peut
commencer**, et **ce qu'on fait quand ça casse**.

## La chaîne en flux

Ces trois règles portent sur ce que `internal/pipeline` garantit. Elles vivent ici parce qu'un
adaptateur n'a pas de `rules.md` (ADR-0010) et qu'`ARCHITECTURE.md` ne déclare pas de code `PIP`
(`N-9`).

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-07 | Le dump brut n'est **jamais matérialisé**, ni en mémoire ni sur disque : dump, compression, chiffrement et empreintes sont chaînés **en une seule passe**. Ce qui est retenu en mémoire reste borné quelle que soit la taille de la base. | `E-025`, § 4.5 `F3.3` | `pipeline/pipeline_test.go › TestBKP07TheStreamIsNeverHeldWhole` |
| BKP-08 | **Deux** empreintes sont calculées au vol : `sha256_raw` sur le dump **avant** compression, `sha256_stored` sur ce qui est réellement écrit. Seule la seconde se vérifie sans déchiffrer, et le lot 3 en dépend. | `E-024`, `N-7` | `pipeline/pipeline_test.go › TestBKP08BothChecksumsAreComputedOnTheWay` |
| BKP-09 | Une erreur **au milieu** du flux — dump interrompu, disque plein, destinataire refusé — remonte **typée** et ne produit **jamais** un résultat qui ressemble à un succès. Un octet écrit n'est pas une sauvegarde. | `E-024`, § 2 `P3` | `pipeline/pipeline_test.go › TestBKP09AFailureMidStreamIsNeverASuccess` |

## Où l'archive est écrite

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-10 | Le chemin d'une archive est **déterministe et lisible par un humain** : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>`. Il se reconstruit **sans la base locale**, pour qu'un dépôt reste exploitable si l'agent disparaît — et il ne contient jamais d'identifiant de connexion. | `E-070`, § 5.5 `F5.5` | `backup/path_test.go › TestBKP10TheArchivePathIsDeterministicAndReadable` |
| BKP-11 | Une écriture interrompue **ne laisse pas d'archive partielle visible** : on écrit à côté, puis on renomme. Un fichier qui porte le nom d'une archive est une archive entière. | `E-066`, § 2 `P3` | `store/storetest › an interrupted write leaves nothing visible` |
| BKP-12 | Tester l'accès à une destination **dit pourquoi** il échoue — répertoire absent, droits, disque plein — et n'écrit rien de durable. | `E-066`, § 5.5 `F5.1` | `store/storetest › checking access` |

## Le dump

Ces règles portent sur ce que `internal/engine` garantit en lançant l'outil. Elles vivent ici pour
la raison de `N-9` : un adaptateur n'a pas de `rules.md`.

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-13 | Le dump sort **sur un flux**, jamais dans un fichier intermédiaire : PostgreSQL en `-Fc` avec `--no-owner --no-privileges`, MySQL et MariaDB avec `--single-transaction`, routines, déclencheurs et événements. Le mot de passe voyage par **l'environnement**, jamais sur la ligne de commande. | `E-054`, `E-056`, `E-115`, `N-3` | `engine/dump_test.go › TestADumpOfPostgreSQLIsAnArchivePgRestoreReads`, `› TestThePostgreSQLDumpCarriesTheOptionsOfTheSpecification`, `› TestTheMySQLFamilyDumpIsTransactionalAndComplete` |
| BKP-14 | **Fermer le dump attend le sous-process** et remonte ce qu'il a dit : un dump dont le process a échoué produit une archive parfaitement formée de rien, et seul le code de retour les distingue. Un dump **abandonné en cours de lecture est arrêté**, pas laissé tourner. C'est aussi ce qui ferme la connexion à la base **avant** que le moindre envoi ne commence. | `E-055`, § 2 `P3` | `engine/dump_test.go › TestADumpThatFailsIsAFailure`, `› TestClosingTheDumpEndsTheConnection`, `› TestClosingAHalfReadDumpStopsIt` |
| BKP-15 | Le format répertoire `-Fd` est **refusé dans la stratégie `exec`**, avec un message qui nomme le conteneur et la sortie — koffr ne sait pas relire un répertoire rempli dans un conteneur comme une archive. Hors conteneur, il **n'est pas un flux** non plus : il s'écrit dans un répertoire de tampon, ce qui est la raison pour laquelle le CDC lui impose le mode `stage`. | `E-054`, ADR-0015, § 5.3 `F3.5` | `engine/dump_test.go › TestTheDirectoryFormatIsRefusedInTheExecStrategy`, `› TestTheDirectoryFormatSaysItNeedsStaging` |
| BKP-16 | Quand une base déclare la stratégie `exec`, **le dump lui-même** passe dans son conteneur, avec le client de son image, et le flux en ressort. La résolution seule ne suffit pas : c'est ce qu'ADR-0015 exige pour un parc mixte MySQL / MariaDB. | `E-046`, ADR-0015 | `engine/dump_test.go › TestAMariaDBIsDumpedByTheClientOfItsOwnContainer`, `› TestAPostgreSQLIsDumpedInsideItsOwnContainer` |

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Niveau de compression | `zstd:3` | `N-4` (hypothèse de `Q-08`) | `pipeline/pipeline_test.go` |
