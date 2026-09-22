# Recette lot 2 — Sauvegarder une base vers un fichier chiffré

C'est la première recette où koffr **fait** quelque chose. Tout ce qui précède préparait cette
question : **l'archive produite est-elle utilisable le jour où on en a besoin, sans koffr, sans la
machine, sans rien d'autre que la clé privée ?**

Cette session tranche aussi **quatre questions ouvertes depuis le démarrage du projet** (`Q-01`,
`Q-04`, `Q-07`, `Q-08`). Elles sont implémentées sous hypothèse ; c'est le moment de les confirmer
sur du concret.

Durée attendue : une heure, préparation des bases non comprise.

## Avant de commencer

- La machine de recette du lot 1, avec son parc : PostgreSQL, MariaDB et MySQL réels, et les
  clients installés selon le `README`.
- **Une paire de clés `age`** générée par `koffr keygen`, dont la privée est mise à l'abri **hors
  de la machine** — c'est tout le sujet d'ADR-0007.
- L'outil **`age` standard** installé (`apt install age`), pour déchiffrer sans koffr.
- Un répertoire de destination, par exemple `/srv/backups`.
- **Une base avec du contenu** : quelques tables, quelques milliers de lignes. Une base vide ne
  prouve rien.
- Prépare : le propriétaire.

## Ce qui est voulu et pourrait passer pour un bug

| Constat | Pourquoi c'est voulu |
| --- | --- |
| Aucun **manifeste** n'est déposé à côté de l'archive | Étapes 06 et 07 de `E-024` : lot 3 |
| `koffr verify` et `koffr list` n'existent pas | Lot 3 |
| Seule la destination `filesystem` fonctionne | S3 et SFTP au lot 4 ; `E-066` demande **l'interface**, et ce lot la prouve avec une implémentation |
| Les **droits et propriétaires** ne sont pas sauvegardés | `--no-owner --no-privileges`, ce que le manifeste du CDC montre (`N-3`, `Q-07`) |
| La clé privée n'est **nulle part** sur la machine | C'est le cœur d'ADR-0007 : un attaquant qui prend la machine n'obtient pas l'historique |
| Les objets globaux du cluster ne sont pas sauvegardés | Hors MVP, `B-01` |
| `koffr backup` ne planifie rien | Le planificateur est au lot 5 |
| L'extension est `.pgc.zst.age`, pas `.pgc` | `A-13` : le nom dit la pile, dans l'ordre où on la défait. `pg_restore` direct dessus échoue **et c'est normal** |
| `koffr keygen` n'affiche qu'une ligne quand on le redirige | Voulu depuis `A-12` : la clé **publique** est le résultat, donc `koffr keygen >> recipients.txt` ajoute une ligne. La privée reste au terminal |
| L'espace réservé est bien plus petit qu'au premier passage | ADR-0016 : le diviseur est passé de 4 à 8, sur les mesures de la première session |
| `doctor` ajoute une ligne quand le client est plus récent que le serveur | `A-15`, `RSV-13` : les archives portent des directives que ce serveur ignore |

## Parcours

### 1. Une clé qui ne traîne pas

1. `koffr keygen` → **on doit voir** une clé publique et une clé privée à l'écran, et un message
   disant que la privée n'est **écrite nulle part**.
2. Chercher la clé privée sur la machine — `grep -r AGE-SECRET-KEY /etc /var` → **on ne doit rien
   trouver**. Attention au faux positif : `sudo` journalise la **ligne de commande**, donc la
   recherche elle-même apparaît dans `auth.log` et dans le journal `systemd`. Chercher la chaîne la
   journalise.
3. Mettre la clé publique dans `recipients.txt`, la privée **ailleurs**.

**Décision attendue** : cette ergonomie est-elle tenable ? Un exploitant qui perd cette fenêtre de
terminal perd la clé. Faut-il un mot d'avertissement plus fort, ou l'écriture vers un chemin qu'il
nomme explicitement ?

### 2. Une seule clé ne suffit pas

1. Ne déclarer **qu'un** destinataire et démarrer koffr → **on doit voir** un avertissement nommant
   le **séquestre** et expliquant ce qu'on risque (`E-132`).
2. Ajouter une seconde clé → **on doit voir** l'avertissement disparaître.

**Décision attendue** : l'avertissement suffit-il, ou faut-il **refuser** de démarrer avec une seule
clé ? Le § 11 dit « obligatoires dès la configuration initiale », ce qui se lit dans les deux sens.

### 3. La sauvegarde, enfin

1. `koffr backup boutique` → **on doit voir** le job se dérouler, et une archive apparaître sous
   `/srv/backups/boutique/2026/09/`.
2. Regarder le **nom** du fichier → **on doit voir** un chemin lisible par un humain, qui dit la
   base, la date et l'identifiant (`E-070`).
3. `koffr backup erp` sur la MariaDB → **on doit voir** la même chose.
4. `koffr backup boutique --dry-run` → **on doit voir** ce qui serait fait, et **rien** de nouveau
   sur le disque.

**Décision attendue** : la sortie de `backup` dit-elle ce qu'il faut ? Que voudriez-vous y lire que
vous n'y lisez pas ?

### 4. L'archive vit sans koffr — le scénario 6 du § 8

C'est le parcours le plus important de la session. Une archive qu'on ne peut pas ouvrir le jour de
l'incident ne vaut rien, et la seule preuve est de l'ouvrir **sans l'outil qui l'a écrite**.

1. `age --decrypt -i cle-privee.txt archive.zst.age > /tmp/dump` → **on doit voir** le
   déchiffrement réussir. **koffr n'intervient pas.**
2. Sans la clé — `age --decrypt -i une-autre-cle.txt` → **on doit voir** un refus.
3. `zstd -d /tmp/dump` puis `pg_restore --list` → **on doit voir** la liste des objets de la base.
4. Restaurer vraiment dans une base vide et **compter les lignes d'une table**, des deux côtés →
   **on doit voir** le même nombre.

**Décision attendue** : aucune. C'est le critère de sortie n° 2, et il passe ou le lot ne passe pas.

### 5. Le tampon fait ce qu'il promet — scénario 11, première moitié

1. Sur la base la plus volumineuse, lancer `koffr backup` et **regarder le disque pendant** :
   `watch du -sh /var/lib/koffr/tmp` → **on ne doit jamais voir** un fichier de la taille du dump
   **brut**, seulement de la taille compressée (`E-025`).
2. Regarder les connexions à la base pendant l'envoi — `SELECT * FROM pg_stat_activity` →
   **on doit voir** la connexion du dump **fermée** avant que l'écriture ne se termine (`E-055`).
3. Relancer avec `staging: stream` → **on doit voir** un comportement différent, et koffr le
   **dire**.

**Décision attendue** : `Q-01` — le mode par défaut. Il est implémenté en `auto` (`N-1`). Après
l'avoir vu tourner : préférez-vous `auto`, qui s'adapte, ou `stage`, qui se comporte pareil partout
au prix d'un disque qui doit toujours suivre ?

### 6. Deux jobs ne se marchent pas dessus

1. Lancer `koffr backup boutique` et, **pendant** qu'il tourne, en lancer un second →
   **on doit voir** le second **refusé**, en nommant le job en cours. Pas mis en file (`E-051`).
2. Tuer brutalement le premier (`kill -9`), puis relancer → **on doit voir** koffr **reprendre** le
   verrou orphelin, pas rester bloqué.

**Décision attendue** : aucune.

### 7. Ce qui manque, et qu'on veut vérifier quand même

1. Chercher un fichier `.json` à côté de l'archive → **il n'y en a pas**. C'est le lot 3, et c'est
   voulu.
2. Vérifier que l'archive **ne contient aucun identifiant de connexion** — la déchiffrer et
   chercher le mot de passe de la base → **on ne doit rien trouver**.

**Décision attendue** : `Q-07` — la portée du dump. Les droits et les propriétaires ne sont pas
sauvegardés (`N-3`). Après avoir restauré au parcours 4 : est-ce acceptable, ou faut-il les
récupérer ?

## Historique

- **2026-09-22, première session** : jouée sur l'instance Multipass `koffr` (Ubuntu 26.04 `arm64`),
  sur un parc réel — PostgreSQL 16.15 (`boutique`, 372 Mo, 1 620 000 commandes, vue, fonction, deux
  index) et MariaDB 11.4.13 (`erp`, 12 Mo, 60 000 factures, procédure, déclencheur), chacune avec un
  utilisateur de sauvegarde à droits restreints. Les sept parcours joués. **Le critère de sortie
  n° 2 est tenu** : l'archive s'ouvre avec `age` et `zstd` seuls, se restaure, et les comptages sont
  identiques des deux côtés — 120 000 commandes et 299 957 982 € pour PostgreSQL, 60 000 factures et
  99 970 009 € pour MariaDB. **Le scénario 11 est tenu et observé** : dump brut 295 Mo, pic du tampon
  23,1 Mo, zéro échantillon où une connexion de dump et une écriture vers la destination coexistent.
  **Sept anomalies, `A-12` à `A-18`**, dont trois bloquantes.

- **2026-09-22, rejeu après corrections** : les **sept parcours** rejoués sur la même instance et le
  même parc, après `docs/plans/lot-2-corrections.md`. **Aucune nouvelle anomalie.** Les sept
  anomalies `A-12` à `A-18` sont vérifiées corrigées sur la machine :
  `koffr backup` écrit 499 octets sur la **sortie standard** ; les archives s'appellent
  `…​.pgc.zst.age` et `…​.sql.zst.age` ; les identifiants sont des **ULID de 26 caractères**
  (`01M2ZGTMYBFF6X3MAYMZWREHQJ`) ; un `kill -9` à 0,7 s laisse `staging-112735-01M2ZG….koffr` de
  4,6 Mo et un verrou orphelin, **tous deux purgés à la relance** ; le journal porte les **sept
  étapes** de `E-024` et aucun secret ; l'avertissement de séquestre apparaît sur `config validate`
  **et** `doctor`. L'espace réservé est passé de 139 Mo à **73 Mo** pour la même base (`Q-08`).
  Le critère de sortie n° 2 est retenu : 1 620 000 commandes et 4 049 903 965 € identiques des deux
  côtés après un aller-retour par `age` et `zstd` seuls, et le scénario 11 est inchangé — pic du
  tampon 23,1 Mo pour 295 Mo de dump, zéro chevauchement.

### Mesures de la session, pour `Q-08`

| Base | Dump brut | Archive | Taux réel | Ce que koffr réservait |
| --- | --- | --- | --- | --- |
| `boutique` (PostgreSQL, 372 Mo) | 295 Mo | 23,1 Mo | **7,8 %** | 139 Mo (`N-12` : base ÷ 4, × 1,5) |
| `erp` (MariaDB, 12 Mo) | 9,4 Mo | 0,37 Mo | **3,9 %** | 4,7 Mo |

Le taux mesuré est **trois à six fois meilleur** que l'hypothèse `N-12` (25 % du brut), et meilleur
que la fourchette 10 %–25 % du § 4.5 elle-même. Conséquence concrète : sur une machine au disque
juste, koffr basculera en `stream` alors que le tampon serait entré six fois.

### Le contraste `stage` / `stream`, mesuré

| Mode | Pic du tampon | Échantillons où le dump et l'envoi coexistent |
| --- | --- | --- |
| `stage` | 23,1 Mo (= l'archive) | **0** |
| `stream` | **0** | **46** |

En `stream`, la transaction reste ouverte sur la production pendant **tout** l'envoi. C'est ce que le
mode échange, et c'est l'argument concret pour `Q-01`.

## Décisions attendues de la session

1. **`Q-01`** — mode de tampon par défaut : `auto` ou `stage` ? (parcours 5)
2. **`Q-04`** — destinataires par base : la liste par base **remplace** la globale (`N-2`). Est-ce
   la bonne règle, ou faut-il qu'elle la complète ?
3. **`Q-07`** — portée du dump, droits et propriétaires (parcours 7).
4. **`Q-08`** — les trois constantes de ce lot : seuil `-Fd` à 20 Go, marge disque ×1,5,
   compression `zstd:3`. Vues à l'œuvre, sont-elles justes ?
5. L'ergonomie de `keygen` et la force de l'avertissement sur le séquestre (parcours 1 et 2).

## Comment rapporter

Une anomalie = écran ou commande, geste, attendu, constaté, gravité (bloquant / gênant /
cosmétique). Dans `docs/recette/anomalies.md`, numérotée à la suite de `A-11`.
