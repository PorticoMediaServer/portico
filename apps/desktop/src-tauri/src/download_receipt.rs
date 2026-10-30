//! Signed offline authority. Neither a filename nor a server preparation alone permits play.
use crate::boundary::{Error, Result};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use ring::signature::{ED25519, UnparsedPublicKey};
use serde::{Deserialize, Serialize};

pub const SAFE_INTEGER: u64 = 9_007_199_254_740_991;

#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DownloadViewer {
    pub authority: String,
    pub account_id: String,
    pub profile_id: String,
    pub server_id: String,
}
pub fn identifier(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 160
        && value
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'_' | b'-'))
}
pub fn digest(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
}
impl DownloadViewer {
    pub fn validate(&self) -> Result<()> {
        if !matches!(self.authority.as_str(), "local" | "hosted")
            || [&self.account_id, &self.profile_id, &self.server_id]
                .iter()
                .any(|v| !identifier(v))
        {
            return Err(Error::InvalidCommand);
        }
        Ok(())
    }
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReceiptEnvelope {
    pub receipt_id: String,
    pub key_id: String,
    pub algorithm: String,
    pub payload: String,
    pub signature: String,
    pub revision: u64,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Claims {
    kind: String,
    version: String,
    receipt_id: String,
    key_id: String,
    viewer: DownloadViewer,
    item_id: String,
    preparation_id: String,
    quality: String,
    artifact: Artifact,
    issued_at: String,
    expires_at: String,
}
#[derive(Deserialize)]
struct Artifact {
    sha256: String,
    bytes: u64,
}
pub struct ReceiptBinding<'a> {
    pub viewer: &'a DownloadViewer,
    pub item_id: &'a str,
    pub preparation_id: &'a str,
    pub quality: &'a str,
    pub sha256: &'a str,
    pub bytes: u64,
}
pub struct VerifiedReceipt {
    pub issued_ms: i64,
    pub expires_ms: i64,
}

/// `public_key` comes only from this viewer's previously authenticated server-key row.
/// It is not taken from the receipt envelope. Signed bytes are verified before parsing claims.
pub fn verify_receipt(
    envelope: &ReceiptEnvelope,
    public_key: &str,
    binding: &ReceiptBinding<'_>,
    now_ms: i64,
) -> Result<VerifiedReceipt> {
    let invalid = || Error::InvalidReceipt;
    binding.viewer.validate()?;
    if envelope.algorithm != "ed25519"
        || !identifier(&envelope.receipt_id)
        || !identifier(&envelope.key_id)
        || envelope.revision == 0
        || envelope.revision > SAFE_INTEGER
        || envelope.payload.len() > 16_384
        || envelope.signature.len() > 128
        || public_key.len() > 128
    {
        return Err(invalid());
    }
    let payload = URL_SAFE_NO_PAD
        .decode(&envelope.payload)
        .map_err(|_| invalid())?;
    let signature = URL_SAFE_NO_PAD
        .decode(&envelope.signature)
        .map_err(|_| invalid())?;
    let key = URL_SAFE_NO_PAD.decode(public_key).map_err(|_| invalid())?;
    if key.len() != 32 || signature.len() != 64 {
        return Err(invalid());
    }
    UnparsedPublicKey::new(&ED25519, key)
        .verify(&payload, &signature)
        .map_err(|_| invalid())?;
    let claims: Claims = serde_json::from_slice(&payload).map_err(|_| invalid())?;
    let issued = chrono::DateTime::parse_from_rfc3339(&claims.issued_at)
        .map_err(|_| invalid())?
        .timestamp_millis();
    let expires = chrono::DateTime::parse_from_rfc3339(&claims.expires_at)
        .map_err(|_| invalid())?
        .timestamp_millis();
    if claims.kind != "portico.download-receipt"
        || claims.version != "1"
        || claims.receipt_id != envelope.receipt_id
        || claims.key_id != envelope.key_id
        || claims.viewer != *binding.viewer
        || claims.item_id != binding.item_id
        || claims.preparation_id != binding.preparation_id
        || claims.quality != binding.quality
        || claims.artifact.sha256 != binding.sha256
        || claims.artifact.bytes != binding.bytes
        || !digest(&claims.artifact.sha256)
        || binding.bytes == 0
        || binding.bytes > SAFE_INTEGER
        || issued < 0
        || expires <= issued
        || now_ms >= expires
    {
        return Err(invalid());
    }
    Ok(VerifiedReceipt {
        issued_ms: issued,
        expires_ms: expires,
    })
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use ring::signature::{Ed25519KeyPair, KeyPair};
    pub fn viewer() -> DownloadViewer {
        DownloadViewer {
            authority: "local".into(),
            account_id: "account".into(),
            profile_id: "profile".into(),
            server_id: "server".into(),
        }
    }
    pub fn signed(viewer: &DownloadViewer, sha: &str, bytes: u64) -> (ReceiptEnvelope, String) {
        let key = Ed25519KeyPair::from_seed_unchecked(&[7; 32]).unwrap();
        let payload = serde_json::to_vec(&serde_json::json!({"kind":"portico.download-receipt","version":"1","receiptId":"receipt","keyId":"key","viewer":viewer,"itemId":"item","preparationId":"preparation","quality":"original","artifact":{"sha256":sha,"bytes":bytes},"issuedAt":"2026-10-23T00:00:00Z","expiresAt":"2026-11-23T00:00:00Z"})).unwrap();
        (
            ReceiptEnvelope {
                receipt_id: "receipt".into(),
                key_id: "key".into(),
                algorithm: "ed25519".into(),
                payload: URL_SAFE_NO_PAD.encode(&payload),
                signature: URL_SAFE_NO_PAD.encode(key.sign(&payload).as_ref()),
                revision: 1,
            },
            URL_SAFE_NO_PAD.encode(key.public_key().as_ref()),
        )
    }
    #[test]
    fn signature_artifact_viewer_and_expiry_are_independent_requirements() {
        let viewer = viewer();
        let sha = "ab".repeat(32);
        let binding = ReceiptBinding {
            viewer: &viewer,
            item_id: "item",
            preparation_id: "preparation",
            quality: "original",
            sha256: &sha,
            bytes: 5,
        };
        let (receipt, key) = signed(&viewer, &sha, 5);
        let now = 1_792_713_600_000;
        assert!(verify_receipt(&receipt, &key, &binding, now).is_ok());
        let mut altered = receipt.clone();
        altered.payload.push('A');
        assert!(verify_receipt(&altered, &key, &binding, now).is_err());
        assert!(
            verify_receipt(
                &receipt,
                &key,
                &ReceiptBinding {
                    bytes: 6,
                    ..binding
                },
                now
            )
            .is_err()
        );
        let other = DownloadViewer {
            profile_id: "other".into(),
            ..viewer.clone()
        };
        assert!(
            verify_receipt(
                &receipt,
                &key,
                &ReceiptBinding {
                    viewer: &other,
                    ..binding
                },
                now
            )
            .is_err()
        );
        assert!(verify_receipt(&receipt, &key, &binding, i64::MAX).is_err());
        assert!(verify_receipt(&receipt, &URL_SAFE_NO_PAD.encode([0; 32]), &binding, now).is_err());
    }
}
