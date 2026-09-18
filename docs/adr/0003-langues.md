# ADR-0003 — Le produit parle anglais, le projet se pilote en français

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-100`, `E-103`, `E-042`, `E-116`
- **Références** : répond à `Q-17` ; `METHODE.md` § Langues et nommage ; lié à ADR-0001

## Contexte

Le CDC ne dit nulle part en quelle langue le produit s'adresse à son utilisateur. Le document est en
français ; tout le vocabulaire du produit est déjà en anglais : clés de configuration, noms
d'événements (`backup_missed`), modes de nettoyage (`drop-schemas`), commandes, champs du manifeste.

L'utilisateur est un exploitant. Ses messages d'erreur finissent collés dans un moteur de recherche
ou dans un ticket — `E-042` exige d'ailleurs un message nommant la version attendue, les versions
trouvées et la commande de correction : c'est du texte destiné à être cherché. Le dépôt sera public
sous Apache-2.0 (ADR-0001).

## Décision

L'interface, la CLI, les messages d'erreur, les journaux et la documentation d'installation sont en
**anglais**. Le pilotage du projet reste en **français**.

- **Anglais** : identifiants, commentaires, noms de branches et de commits, tables et colonnes
  (règle déjà posée par `METHODE.md`), et en plus ici : textes de l'interface web, sorties de la CLI,
  messages d'erreur, journaux, `README`, documentation d'installation et d'exploitation (`E-116`).
- **Français** : `CLAUDE.md`, `ARCHITECTURE.md`, `ROADMAP.md`, `METHODE.md`, ADR, plans, registres
  (`E-nnn`, `Q-nn`, `D-nn`, `B-nn`, `A-nn`), rétrospectives, `rules.md`, skills.
- **Aucun mécanisme de traduction** dans le MVP : pas de catalogue de messages, pas de `i18n`. Les
  chaînes sont écrites directement dans le code.

## Conséquences

- Un `rules.md` en français décrit un code dont les messages sont en anglais : la règle cite le
  message anglais entre guillemets quand le texte exact fait partie de la règle (`E-042`).
- La recette (`docs/recette/`) se joue en français sur une interface en anglais. C'est inconfortable
  et assumé ; les scénarios citent les libellés anglais tels qu'ils s'affichent.
- Ajouter le français plus tard coûtera l'extraction de toutes les chaînes. C'est un coût connu,
  accepté contre l'absence de mécanisme de traduction dans un MVP.
- **Ce qui rouvrirait la décision** : un utilisateur référent (`D-01`) non anglophone, ou une
  diffusion finalement interne et francophone.

## Alternatives écartées

- **Interface en français** : plus confortable pour un usage interne, mais produit un mélange
  permanent à l'écran (`tool_missing` expliqué en français) et une bascule coûteuse à l'ouverture du
  dépôt.
- **Anglais avec `i18n` prête dès le départ** : un mécanisme de traduction à porter dans l'interface
  et la CLI dès le premier lot, pour un bénéfice hypothétique.
