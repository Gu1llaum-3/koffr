# ADR-0001 — Le produit s'appelle koffr, sous licence Apache-2.0, dans deux dépôts

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-017`, `E-026`, `E-103`, `E-105`
- **Références** : tranche `D-02` et `D-08` ; CDC § 11.1 ; lié à ADR-0003 (langues), ADR-0010 (modules)

## Contexte

Le CDC annonce « Keeper » comme un nom de travail et demande de trancher le nom et la licence avant
`L0` (§ 11.1). Le nom n'est pas cosmétique : il fixe le chemin du module Go, le nom du binaire, les
chemins prescrits par `E-026` (`/etc/keeper/`, `/var/lib/keeper/`, `/var/log/keeper/`), le nom de
l'unité `systemd` et celui de la commande. Le changer après le premier lot livré casse la
configuration des utilisateurs existants.

Deux faits pèsent sur le choix. « Keeper Security » est une marque déposée du logiciel de sécurité
(gestion de mots de passe) : la catégorie diffère, le risque juridique est faible, mais la
découvrabilité d'un « keeper backup » est mauvaise. Et le produit est en réalité **deux** logiciels :
l'agent, objet de ce CDC, et le serveur central de supervision de parc, qui aura le sien (§ 9, `L5`).

Le CDC fixe déjà le vocabulaire du second : la section de configuration s'appelle `server`
(`server.enabled`, `server.url`, `server.token_file`), le composant qui lui parle s'appelle `uplink`,
et le texte dit partout « serveur central ». Par ailleurs `koffr ui` désigne déjà l'interface web
**locale de l'agent** (`E-103`, `F11.2`) : le mot « ui » n'est pas disponible pour nommer le serveur.

## Décision

Le produit s'appelle **koffr**. L'agent est `koffr`, le serveur central est `koffr-server`, sous
licence **Apache-2.0**, dans **deux dépôts distincts**.

- Binaire `koffr` ; chemins `/etc/koffr/koffr.yaml`, `/etc/koffr/recipients.txt`,
  `/var/lib/koffr/koffr.db`, `/var/lib/koffr/tools/`, `/var/lib/koffr/tmp/`,
  `/var/log/koffr/koffr.log` ; unité `koffr.service`. `E-026` est reformulée en conséquence.
- Dépôt `koffr` (cet agent) et dépôt `koffr-server` (le serveur central, hors MVP).
- Le **protocole** de liaison vit dans un paquet **public** du dépôt de l'agent, importable par le
  serveur ; il n'importe rien du domaine de l'agent (règle de dépendance dans ADR-0010).
- Licence Apache-2.0 sur les deux dépôts, en-tête de licence non obligatoire dans chaque fichier,
  fichier `LICENSE` et `NOTICE` à la racine.
- Le CDC reçu n'est **pas** modifié : il reste le document d'origine dans `docs/cdc/`. La
  correspondance « Keeper → koffr » est portée par cet ADR et par le glossaire de `CLAUDE.md`.

## Conséquences

- Le chemin du module Go est figé au lot 0 et n'est plus renégociable sans casser les imports.
- Un dépôt séparé pour le serveur oblige à publier le protocole comme une interface stable, avec sa
  propre compatibilité ascendante. C'est le coût direct de la frontière de confiance du § 3 — et
  c'est précisément ce qu'on achète : le serveur ne peut pas « simplement » partager un type interne
  avec l'agent.
- Avant de figer le nom au lot 0, vérifier la disponibilité : organisation GitHub, `pkg.go.dev`,
  Docker Hub, Homebrew, nom de domaine. Prévoir une redirection depuis les orthographes probables
  (`coffr`, `coffre`, `koffer`).
- Apache-2.0 autorise un usage commercial et n'empêche pas de publier plus tard `koffr-server` sous
  une autre licence : l'auteur des deux est le même.
- **Ce qui rouvrirait la décision** : une opposition de marque, ou la décision de fusionner agent et
  serveur dans un seul binaire — ce qui contredirait `P5` (le serveur aura des dépendances de
  service que l'agent s'interdit).

## Alternatives écartées

- **Garder « Keeper »** : découvrabilité mauvaise à côté d'une marque établie du même secteur.
- **`koffr-ui` pour le serveur** : collision directe avec `koffr ui`, l'interface locale de l'agent.
- **`koffr-central`** : introduit un troisième mot pour ce que la configuration appelle déjà `server`.
- **Deux noms sans lien** : double le travail de notoriété, rien ne dit qu'ils vont ensemble.
- **Dépôt unique (monorepo)** : la facilité de partager du code est ce que le § 3 décrit comme
  contaminant pour le modèle de l'agent.
- **AGPL-3.0** : la clause réseau ne se déclenche presque jamais pour un démon local, et elle fait
  reculer les services juridiques d'entreprise, soit exactement la cible du § 1.1.
- **MIT** : équivalent en pratique, sans clause de brevets explicite.
- **Source fermée** : incompatible avec l'objectif de diffusion du § 1.1.
