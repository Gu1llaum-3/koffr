# Règles — module `catalog` (code `CAT`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module **indexe**. Il ne sauvegarde pas, ne vérifie pas, n'écrit sur aucune destination. Et il
n'est **jamais la source de vérité** : ce qu'une restauration exige vit dans le manifeste déposé à
côté de l'archive, pour qu'un dépôt reste exploitable quand l'agent, son disque et son SQLite ont
disparu.

## L'instantané de configuration

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CAT-01 | Le catalogue garde un **instantané de la configuration résolue** de chaque base, avec son empreinte, pour qu'un changement se remarque. Deux configurations identiques donnent la **même** empreinte ; tout changement qui compte — hôte, port, utilisateur, moteur, destinations, tampon, planning, conteneur — la change. L'instantané ne porte **aucun identifiant de connexion**, et les champs émis sont **énumérés** : en ajouter un est une décision, pas une édition. | `E-028`, `E-115`, § 4.4 | `catalog/fingerprint_test.go › TestCAT01TheSameConfigurationGivesTheSameFingerprint`, `› TestCAT01AnyChangeThatMattersChangesTheFingerprint`, `› TestCAT01TheSnapshotCarriesNoCredential` |

## Le catalogue est un index

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CAT-02 | **Tout ce qu'une restauration exige survit dans le manifeste**, sans le catalogue : identifiant, base, moment, tailles, empreintes, état de vérification, et ce qui dit **comment ouvrir** l'archive — format, chaîne de traitement, outil et sa version, destinataires. Le test le prouve de la seule façon qui vaille : il construit le manifeste, jette le catalogue, et reconstruit l'entrée depuis le manifeste seul. L'état de `P4` voyage avec : une archive que personne n'a vérifiée ne revendique rien. | ADR-0006, `E-059`, `E-058` | `catalog/manifest_test.go › TestCAT02EverythingARestoreNeedsSurvivesInTheManifest`, `› TestCAT02TheVerificationStateTravelsWithTheArchive` |

## Le manifeste

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CAT-03 | Le manifeste porte **exactement** les champs du § 5.3, sous les noms qu'il montre — énumérés, pas comptés : un compte ne dit pas lequel manque. Il n'en porte **aucun de plus** : c'est un format que d'autres lisent. Les moments sont en RFC 3339 **UTC** (ADR-0006), et `tool` porte son `argv`, qui dit ce qui saura relire l'archive. | `E-058`, § 5.3 | `catalog/manifest_test.go › TestCAT03TheManifestCarriesTheFieldsOfTheSpecification`, `› TestCAT03TheMomentsAreRFC3339UTC` |
| CAT-04 | Le manifeste ne porte **jamais** d'identifiant de connexion, sous aucune forme — mot de passe, utilisateur, hôte, port, chemin de secret, clé privée, chaîne de connexion. L'`argv` qu'il montre décrit l'**archive** (format, portée), jamais la connexion. Les destinataires y sont : ce sont des clés **publiques**, et elles disent quelle clé ouvre l'archive. | `E-059`, `E-113`, `E-114`, § 6 | `catalog/manifest_test.go › TestCAT04TheManifestCarriesNoCredential`, `› TestCAT04AStolenRepositoryGivesUpMetadataOnly`, `engine/dump_test.go › TestTheArchiveOptionsCarryNothingOfTheConnection`, `cli/endtoend_test.go › TestTheManifestIsDepositedAndReadsWithoutAKey` |
| CAT-05 | Le catalogue sait lire **ce qu'une destination tient réellement**, par un port `Repository` qui lui est propre — jamais le type d'entrée que `backup` déclare pour le sien (`AR-03`). Le port rend des **octets** pour le manifeste : la forme d'un manifeste appartient au domaine, un adaptateur n'a pas à la connaître. Une liste est **ordonnée**, la plus récente d'abord, et l'identifiant départage les ex æquo. | `E-103c`, `N-2` | `catalog/lister_test.go › TestCAT05ARepositoryListingIsMostRecentFirst` |
| CAT-06 | **Une archive que la destination tient et que le catalogue ignore est montrée**, et dit qu'elle n'est pas au catalogue. Le manifeste la renseigne quand il est là ; l'état de vérification, **non** — koffr n'a aucune trace de l'avoir contrôlée, et `E-064` interdit un blanc qui se lirait comme un succès. La fusion ne va que dans un sens : rien de ce qui est trouvé sur une destination n'est écrit au catalogue (`N-1`, ADR-0006). Et l'inverse se voit aussi : une ligne de catalogue dont le fichier a quitté la destination le dit. | `E-064`, `A-19`, `N-1` | `catalog/lister_test.go › TestCAT06AnArchiveTheCatalogueIgnoresIsStillShown`, `› TestCAT06TheManifestFillsInWhatTheNameCannotSay`, `cli/list_test.go › TestListShowsAnArchiveTheCatalogueNeverRecorded` |
| CAT-07 | **Une destination que la configuration ne déclare pas est un refus**, qui nomme celles qui existent. Répondre « rien » à une faute de frappe se lit exactement comme un dépôt vide, avec le code de retour d'un succès. Un dépôt réellement vide, lui, reste un succès. | `A-27`, `E-103c` | `catalog/lister_test.go › TestCAT07AnUnknownDestinationIsRefused`, `› TestAnEmptyRepositoryIsNotAnError`, `cli/list_test.go › TestListRefusesADestinationNobodyDeclared` |
| CAT-08 | **Le manifeste porte l'heure dans le fuseau déclaré**, avec son décalage, comme le § 5.3 l'écrit (`2026-09-18T02:00:03+02:00`). La base garde l'UTC et une seule représentation ; la conversion se fait à l'écriture du manifeste et à l'affichage. Un fuseau à décalage non entier (`+05:45`) est couvert : c'est là qu'un format naïf casse. Et un manifeste écrit dans un fuseau se relit au **même instant**. | ADR-0006, § 5.3, `A-23` | `catalog/manifest_test.go › TestCAT08TheManifestRendersTheDeclaredZone`, `› TestAManifestReadsBackToTheSameInstant` |
| CAT-09 | Le champ `format` porte le nom du § 5.3 — **`pg_custom`**, `sql` — et **non l'extension du fichier**. Une archive s'appelle `.pgc.zst.age` et son format se nomme `pg_custom` : ce sont deux choses, et laisser une valeur servir aux deux était `A-22`. | `E-058`, § 5.3, `A-22`, `N-4` | `catalog/manifest_test.go › TestCAT09TheFormatIsNamedAsTheSpecificationNamesIt` |

Le dépôt lui-même est une garantie de `backup` : voir `BKP-23` dans `internal/domain/backup/rules.md`.

## Ce que le catalogue écrit

Écrit par `internal/state` sur la base SQLite d'`E-028` ; les règles ci-dessus sont pures, ce qui
suit est vérifié sur une **vraie** base.

| Constat | Tenu par |
| --- | --- |
| Enregistrer une base deux fois la **met à jour**, ne la refuse pas — une configuration est relue à chaque exécution | `state/catalog_test.go › TestADatabaseIsRecordedThenUpdated` |
| Une sauvegarde dont la base n'a jamais été enregistrée est **refusée par la clé étrangère**, pas par un contrôle en Go qu'on peut oublier d'écrire | `› TestABackupOfAnUnknownDatabaseIsRefused` |
| Une sauvegarde fraîchement écrite est `none` — `P4` : elle n'est pas vérifiée tant qu'elle ne l'est pas | `› TestABackupIsRecordedWithItsLocations` |
| Un état de vérification que le schéma ne connaît pas est refusé **à l'insertion** (ADR-0006) | `› TestAnUnknownVerificationStateIsRefused` |
| Vérifier une sauvegarde qui n'existe pas est une **erreur typée**, jamais un silence | `› TestSettingTheStateOfAnUnknownBackupIsAnError` |
| Le listing rend les plus récentes d'abord — un exploitant compare deux relevés | `› TestBackupsComeBackMostRecentFirst` |

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Empreinte de configuration | SHA-256 du JSON résolu | `E-028`, `CAT-01` | `catalog/fingerprint_test.go` |
| État initial d'une archive | `none` | § 2 `P4` | `state/catalog_test.go` |
