---
title: Telegram Audio Input
description: Voice-note download, OpenAI transcription, semantic normalization, persistence, and Telegram acknowledgement flow.
methods:
  - audioinput.Processor.Process: Transcribes and normalizes one bounded recording.
  - telegram.Handler.downloadTelegramAudio: Downloads authorized Telegram audio with a size limit.
  - telegram.audioConfirmationText: Formats the first permanent acknowledgement message.
depends_on:
  - internal/audioinput/processor.go
  - internal/telegram/audio.go
  - internal/telegram/handler.go
  - internal/telegram/progress.go
used_by:
  - cmd/bot/main.go
---

# Telegram Audio Input

Authorized users can send Telegram voice notes or audio attachments as chatbot
input. Audio follows the same conversation and tool-calling path as typed text
after it has been converted into a canonical user message.

## Processing flow

1. The handler posts a temporary `🎧 Escoltant l’àudio…` message.
2. Telegram file metadata is resolved and the file is downloaded into bounded
   memory. Files larger than 25,000,000 bytes are rejected before OpenAI is
   called whenever Telegram supplies the size.
3. `audioinput.Processor` calls the OpenAI transcription endpoint with
   `OPENAI_TRANSCRIPTION_MODEL`.
4. A second Responses API call uses the configured `OPENAI_MODEL` and a strict
   JSON schema to remove disfluencies, apply explicit self-corrections, and
   preserve material travel details.
5. Only the normalized `canonical_message` is persisted with the `user` role.
   The audio binary and raw transcript are not persisted.
6. The listening status becomes the first permanent reply:
   `🎙️ M’has dit que …`.
7. A separate `💭 Pensant…` progress message is created and replaced by the
   chatbot's normal answer.

The acknowledgement is intentionally not stored as an assistant conversation
message because it only repeats the canonical user message. The final chatbot
answer is persisted normally.

## Failure behavior

- Authorization runs before any Telegram download or OpenAI request.
- A download or transcription failure replaces the listening status with an
  actionable error and does not persist a user message.
- A normalization failure also rejects the input. Raw speech is never inserted
  into the conversation context as a fallback.
- Once normalization and persistence succeed, the acknowledgement remains
  visible even if final response generation later fails.

## Configuration

`OPENAI_TRANSCRIPTION_MODEL` defaults to `gpt-transcribe`. Transcript
normalization uses `OPENAI_MODEL`, while both operations reuse
`OPENAI_API_KEY` and `OPENAI_BASE_URL`.
