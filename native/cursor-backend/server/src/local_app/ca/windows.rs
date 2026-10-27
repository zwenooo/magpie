//! Implements Windows-specific certificate authority integration.
//! Native Windows system root-store access without external command-line tools.

use std::{ffi::c_void, io, ptr, slice};

use windows_sys::Win32::Security::Cryptography::{
    CertAddEncodedCertificateToStore, CertCloseStore, CertEnumCertificatesInStore, CertOpenStore,
    CERT_STORE_ADD_REPLACE_EXISTING, CERT_STORE_OPEN_EXISTING_FLAG, CERT_STORE_PROV_SYSTEM_W,
    CERT_STORE_READONLY_FLAG, CERT_SYSTEM_STORE_CURRENT_USER, CERT_SYSTEM_STORE_LOCAL_MACHINE,
    PKCS_7_ASN_ENCODING, X509_ASN_ENCODING,
};

use crate::{Error, Result};

const ROOT_STORE: [u16; 5] = [b'R' as u16, b'O' as u16, b'O' as u16, b'T' as u16, 0];

pub(super) fn is_installed(cert: &str) -> Result<bool> {
    Ok(is_installed_in(cert, CERT_SYSTEM_STORE_CURRENT_USER)?
        || is_installed_in(cert, CERT_SYSTEM_STORE_LOCAL_MACHINE)?)
}

fn is_installed_in(cert: &str, location: u32) -> Result<bool> {
    let der = certificate_der(cert)?;
    let store = open_root_store(location, true)?;
    let mut context = ptr::null();
    let mut found = false;
    loop {
        context = unsafe { CertEnumCertificatesInStore(store, context) };
        if context.is_null() {
            break;
        }
        let encoded = unsafe {
            slice::from_raw_parts((*context).pbCertEncoded, (*context).cbCertEncoded as usize)
        };
        if encoded == der {
            found = true;
            break;
        }
    }
    if !context.is_null() {
        unsafe { windows_sys::Win32::Security::Cryptography::CertFreeCertificateContext(context) };
    }
    close_store(store)?;
    Ok(found)
}

pub(super) fn trust_current_user(cert: &str) -> Result<()> {
    let der = certificate_der(cert)?;
    let store = open_root_store(CERT_SYSTEM_STORE_CURRENT_USER, false)?;
    let result = unsafe {
        CertAddEncodedCertificateToStore(
            store,
            X509_ASN_ENCODING | PKCS_7_ASN_ENCODING,
            der.as_ptr(),
            der.len() as u32,
            CERT_STORE_ADD_REPLACE_EXISTING,
            ptr::null_mut(),
        )
    };
    let error = if result == 0 {
        Some(io::Error::last_os_error())
    } else {
        None
    };
    close_store(store)?;
    if let Some(error) = error {
        return Err(Error::Config(format!(
            "trust Magpie Cursor CA for current user: {error}"
        )));
    }
    Ok(())
}

fn certificate_der(cert: &str) -> Result<Vec<u8>> {
    pem::parse(cert)
        .map(|pem| pem.into_contents())
        .map_err(|error| Error::Config(format!("parse CA PEM: {error}")))
}

fn open_root_store(location: u32, readonly: bool) -> Result<*mut c_void> {
    let flags = location
        | CERT_STORE_OPEN_EXISTING_FLAG
        | if readonly {
            CERT_STORE_READONLY_FLAG
        } else {
            0
        };
    let store = unsafe {
        CertOpenStore(
            CERT_STORE_PROV_SYSTEM_W,
            0,
            0,
            flags,
            ROOT_STORE.as_ptr().cast(),
        )
    };
    if store.is_null() {
        return Err(Error::Config(format!(
            "open Windows Root store: {}",
            io::Error::last_os_error()
        )));
    }
    Ok(store)
}

fn close_store(store: *mut c_void) -> Result<()> {
    if unsafe { CertCloseStore(store, 0) } == 0 {
        return Err(Error::Config(format!(
            "close Windows certificate store: {}",
            io::Error::last_os_error()
        )));
    }
    Ok(())
}
