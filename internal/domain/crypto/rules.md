# Règles — module `crypto` (code `CRY`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module connaît les **destinataires** d'une archive, et rien d'autre. Il ne chiffre pas — c'est
`internal/pipeline` — et il ne voit jamais une clé privée : ADR-0007 la met hors de la machine, et
c'est ce qui fait qu'un attaquant qui prend l'agent n'obtient pas l'historique des sauvegardes.

## Destinataires

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CRY-01 | Une clé publique `age` valide est acceptée ; une clé mal formée est une **erreur qui nomme le fichier et la ligne**. Les lignes vides et les commentaires `#` sont ignorés. Un fichier de destinataires **vide ou absent** est une erreur, jamais un chiffrement pour personne. | `E-072`, § 5.6 `F6.1` | `crypto/recipients_test.go › TestCRY01AKeyIsAcceptedOrRefusedWithItsLine`, `› TestCRY01AnEmptyRecipientsFileIsAnError` |
| CRY-02 | **Au moins deux destinataires** sont attendus — une clé opérationnelle et une clé de séquestre. Une seule clé déclarée produit un **avertissement** qui nomme le séquestre et dit ce qu'on risque ; elle n'empêche pas de fonctionner. | `E-073`, `E-132`, § 11 | `crypto/recipients_test.go › TestCRY02ASingleRecipientWarnsAboutTheEscrowKey`, `cli/backup_test.go › TestValidateWarnsAboutASingleRecipient`, `› TestValidateSaysNothingAboutTwoRecipients`, `cli/backup_test.go › TestTheEscrowWarningAppearsOnEveryCommandThatReadsTheKeys` |
| CRY-03 | Le chiffrement est **en flux** : l'entrée n'est jamais matérialisée, et l'archive produite est **déchiffrable par l'outil `age` standard**, sans koffr. Aucun format maison. | `E-072`, `E-075`, § 5.6 `F6.1` et `F6.4` | `pipeline/encrypt_test.go › TestCRY03TheArchiveIsReadableByTheStandardAgeTool`, `› TestCRY03NothingIsMaterialised` |
| CRY-04 | Une archive se déchiffre avec **chacun** des destinataires déclarés, et avec **aucune autre** clé : une clé perdue ne condamne pas les archives, et une clé volée ailleurs n'ouvre rien. | `E-073`, `E-074`, ADR-0007 | `pipeline/encrypt_test.go › TestCRY04EachRecipientCanOpenTheArchive`, `› TestCRY04AnotherKeyOpensNothing` |

## Divergences avec le cahier des charges

- **Une seule clé avertit, elle n'empêche pas.** Le § 11 écrit « destinataires multiples
  **obligatoires** dès la configuration initiale », ce qui se lit dans les deux sens. `E-132` parle
  d'un « avertissement au premier démarrage », et c'est cette lecture qui est retenue : refuser de
  démarrer sur une configuration par ailleurs valide punirait l'exploitant au lieu de l'aider.
  **Annoncé à la recette du lot 2**, parcours 2, comme décision attendue.

## Non porté

- **La clé privée**, nulle part : ni dans la configuration, ni sur la machine, ni en mémoire.
  `koffr keygen` l'affiche une fois et ne l'écrit pas (ADR-0007, `E-076`).
- **Le séquestre lui-même** — où la seconde clé est gardée, par qui — est une procédure
  d'exploitation, documentée dans le `README`, pas du code.
